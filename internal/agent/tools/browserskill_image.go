package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg" // Register JPEG decoding for screenshot validation.
	_ "image/png"  // Register PNG decoding for screenshot validation.

	"github.com/Tencent/WeKnora/internal/types"
)

// Keep binary data out of model text. Data serves the result card, Images feeds
// the vision path, and Output contains only bounded screenshot metadata.
func browserScreenshotResult(raw json.RawMessage) *types.ToolResult {
	invalid := func() *types.ToolResult {
		return &types.ToolResult{
			Success: false,
			Error: "Browser screenshot is invalid or too large; " +
				"retry with a fresh element ref for a smaller capture.",
		}
	}
	var capture struct {
		Image   string          `json:"image_base64"`
		Format  string          `json:"format"`
		TabID   int64           `json:"tab_id"`
		Dialogs json.RawMessage `json:"dialogs"`
	}
	if json.Unmarshal(raw, &capture) != nil || (capture.Format != "png" && capture.Format != "jpeg") ||
		len(capture.Image) == 0 || len(capture.Image) > 8*1024*1024 {
		return invalid()
	}
	data, err := base64.StdEncoding.DecodeString(capture.Image)
	if err != nil {
		return invalid()
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != capture.Format || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > 40_000_000 {
		return invalid()
	}
	meta := map[string]any{"tab_id": capture.TabID, "format": format, "width": config.Width, "height": config.Height}
	if len(capture.Dialogs) > 0 {
		meta["dialogs"] = capture.Dialogs
	}
	output, _ := json.Marshal(meta)
	meta["image_base64"] = capture.Image
	return &types.ToolResult{
		Success: true, Output: string(output), Data: meta,
		Images: []string{"data:image/" + format + ";base64," + capture.Image},
	}
}
