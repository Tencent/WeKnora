package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

func (h *InitializationHandler) checkVisionModelConnection(
	ctx context.Context, model *types.Model, appID, appSecret string,
) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	instance, err := vlm.NewVLM(vlm.ConfigFromModel(model, appID, appSecret), h.ollamaService)
	if err != nil {
		return false, fmt.Sprintf("创建视觉模型实例失败: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	red := image.NewUniform(color.RGBA{R: 220, A: 255})
	draw.Draw(img, image.Rect(24, 24, 104, 104), red, image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return false, fmt.Sprintf("无法生成视觉模型测试图片: %v", err)
	}

	content, err := instance.PredictWithOptions(ctx, [][]byte{encoded.Bytes()},
		"Describe the main shape and its color in a few words.", &vlm.PredictOptions{RejectTruncated: true})
	if errors.Is(err, vlm.ErrTruncatedCompletion) {
		return false, "视觉模型回复被截断，请检查输出额度或模型配置"
	}
	if err != nil {
		return false, fmt.Sprintf("%s：%v", classifyConnectionError(err.Error()), err)
	}
	if strings.TrimSpace(content) == "" {
		return false, "视觉模型未返回有效回复"
	}
	return true, "连接正常，模型可用"
}
