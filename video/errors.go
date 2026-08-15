package video

import "errors"

// ErrRecipeInvalid は、Recipe.Validate が検出した構造上の問題
// （タイトル欠落・カット無し・範囲外の section_index）を包むセンチネルです。
// AI 生成スクリプトの検証では「再生成すれば直るかもしれない失敗」を、
// 通信エラーなどと区別するために使えます。
//
// 他のセンチネルが ports にあるのに対しこれだけこちらにあるのは、返すのが
// このパッケージの Validate であり、recipe は最下層で ports を import できない
// ためです（ports が recipe を import します）。
var ErrRecipeInvalid = errors.New("video recipe is invalid")
