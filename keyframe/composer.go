// Package keyframe は、キャラクターやカット情報から動画のキーフレーム画像を
// 生成・合成するロジックを提供します。
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
	character map[string]string // ReferenceURL -> FileAPIURI
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

// GetCharacterResourceURI はキャラクターの既定参照画像（ReferenceURL）の画像URIを取得します。
// アスペクト比別の参照画像（ReferenceURLs）を取得するには GetResourceURI を使ってください。
func (c *Composer) GetCharacterResourceURI(charID string) string {
	char := c.Characters.GetCharacterWithDefault(charID)
	if char == nil {
		return ""
	}
	return c.GetResourceURI(char.ReferenceURL)
}

// GetResourceURI は、指定した参照画像URL（ReferenceURL または ReferenceURLs の値）に対応する
// File API 上の画像URIを取得します。PrepareCharacterResources で事前アップロード済みである必要が
// あります（未準備、または Vertex AI + GCS URI でアップロードをバイパスした場合は空文字を返し、
// 呼び出し側は referenceURL 自体をそのまま参照として使うことになります）。
func (c *Composer) GetResourceURI(referenceURL string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resourceMap.character[referenceURL]
}

// PrepareCharacterResources はカットに使用される全キャラクターの画像を File API に事前アップロード
// します。各キャラクターの ReferenceURL（既定のフォールバック）と ReferenceURLs（アスペクト比別）の
// 両方に含まれる参照画像URLをすべて対象にします。
func (c *Composer) PrepareCharacterResources(ctx context.Context, cuts []ports.Cut) error {
	targets := make(map[string]string)
	addCharacterURLs := func(char *characterkit.Character) {
		if char == nil {
			return
		}
		if char.ReferenceURL != "" {
			targets[char.ReferenceURL] = char.ReferenceURL
		}
		for _, url := range char.ReferenceURLs {
			if url != "" {
				targets[url] = url
			}
		}
	}

	// デフォルトキャラクターをアップロード対象に追加
	addCharacterURLs(c.Characters.GetDefault())

	// カットで使用されているキャラクターをアップロード対象に追加
	for _, id := range ports.Cuts(cuts).UniqueCharacterIDs() {
		addCharacterURLs(c.Characters.GetCharacterWithDefault(id))
	}

	return c.prepareResources(ctx, targets, c.getOrUploadAsset, "character")
}

// getOrUploadAsset はキャラクター用アセットをキャッシュ制御しつつ取得またはアップロードします。
// key と referenceURL は常に同じ値（参照画像URL自体）です。resourceMap がURLをキーにしている
// ため、渡す2引数は同一になります。
func (c *Composer) getOrUploadAsset(ctx context.Context, key, referenceURL string) (string, error) {
	return c.getOrUploadResource(ctx, key, referenceURL, c.resourceMap.character)
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
