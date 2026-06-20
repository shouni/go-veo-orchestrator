package keyframe

import (
	"context"
	"fmt"
	"sync"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// Composer はキャラクター参照画像のリソース準備と参照 URI の管理を担当します。
type Composer struct {
	AssetManager    imagePorts.AssetManager
	BackendProvider imagePorts.Backend
	Characters      *characterkit.Characters
	resourceMap     resourceMap
	mu              sync.RWMutex
	uploadGroup     singleflight.Group
}

type resourceMap struct {
	character map[string]string // CharacterID -> FileAPIURI
}

// NewComposer は Composer の新しいインスタンスを初期化済みの状態で生成します。
func NewComposer(
	assetMgr imagePorts.AssetManager,
	backend imagePorts.Backend,
	cm *characterkit.Characters,
) (*Composer, error) {
	if assetMgr == nil {
		return nil, fmt.Errorf("assetMgr is required")
	}
	if backend == nil {
		return nil, fmt.Errorf("backend is required")
	}
	if cm == nil {
		return nil, fmt.Errorf("characters is required")
	}

	return &Composer{
		AssetManager:    assetMgr,
		BackendProvider: backend,
		Characters:      cm,
		resourceMap: resourceMap{
			character: make(map[string]string),
		},
	}, nil
}

// GetCharacterResourceURI はキャラクターの画像URIを取得します。
func (c *Composer) GetCharacterResourceURI(charID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resourceMap.character[charID]
}

// PrepareCharacterResources はカットに使用される全キャラクターの画像を File API に事前アップロードします。
func (c *Composer) PrepareCharacterResources(ctx context.Context, cuts []ports.Cut) error {
	targets := make(map[string]string)

	// デフォルトキャラクターをアップロード対象に追加
	if def := c.Characters.GetDefault(); def != nil && def.ReferenceURL != "" {
		targets[def.ID] = def.ReferenceURL
	}

	// カットで使用されているキャラクターをアップロード対象に追加
	for _, id := range ports.Cuts(cuts).UniqueCharacterIDs() {
		char := c.Characters.GetCharacterWithDefault(id)
		if char == nil || char.ReferenceURL == "" {
			continue
		}
		targets[char.ID] = char.ReferenceURL
	}

	return c.prepareResources(ctx, targets, c.getOrUploadAsset, "character")
}

// getOrUploadAsset はキャラクター用アセットをキャッシュ制御しつつ取得またはアップロードします。
func (c *Composer) getOrUploadAsset(ctx context.Context, charID, referenceURL string) (string, error) {
	return c.getOrUploadResource(ctx, charID, referenceURL, c.resourceMap.character)
}

// prepareResources は指定されたリソースを事前アップロードします。
func (c *Composer) prepareResources(
	ctx context.Context,
	targets map[string]string,
	upload func(context.Context, string, string) (string, error),
	resourceType string,
) error {
	eg, egCtx := errgroup.WithContext(ctx)

	for key, referenceURL := range targets {
		eg.Go(func() error {
			if _, err := upload(egCtx, key, referenceURL); err != nil {
				return fmt.Errorf("%s resource preparation failed for '%s': %w", resourceType, key, err)
			}
			return nil
		})
	}

	return eg.Wait()
}

// getOrUploadResource は二重チェックロッキングと singleflight を用いてアセットアップロードの共通ロジックを提供します。
func (c *Composer) getOrUploadResource(ctx context.Context, key, referenceURL string, resourceMap map[string]string) (string, error) {
	// Vertex AI モード時は Cloud Storage (gs://) を直接参照可能なため、
	// File API へのアップロード処理をバイパスし、転送コストを削減します。
	if c.BackendProvider.IsVertexAI() && IsGCSURI(referenceURL) {
		c.mu.RLock()
		_, ok := resourceMap[key]
		c.mu.RUnlock()

		if !ok {
			c.mu.Lock()
			resourceMap[key] = ""
			c.mu.Unlock()
		}
		return "", nil
	}

	// 最初のチェック: ロックを最小限にするための RLock
	c.mu.RLock()
	uri, ok := resourceMap[key]
	c.mu.RUnlock()
	if ok {
		return uri, nil
	}

	// 同一キーに対する同時リクエストを1つに集約（HTTP URL等の場合のみ）
	val, err, _ := c.uploadGroup.Do(key, func() (interface{}, error) {
		c.mu.RLock()
		existingURI, ok := resourceMap[key]
		c.mu.RUnlock()
		if ok {
			return existingURI, nil
		}

		// ここで実際に File API (Google AI Studio) へアップロードされる
		uploadedURI, uploadErr := c.AssetManager.UploadFile(ctx, referenceURL)
		if uploadErr != nil {
			return nil, uploadErr
		}

		c.mu.Lock()
		resourceMap[key] = uploadedURI
		c.mu.Unlock()
		return uploadedURI, nil
	})

	if err != nil {
		return "", err
	}

	return val.(string), nil
}
