package utils

// CalculateTaskCost 估算文档处理的算力消耗成本评估
func CalculateTaskCost(fileSize int64, fileType string, enableMultimodal bool) int64 {
	baseCost := (fileSize / (1024 * 1024)) * 10
	if baseCost < 10 {
		baseCost = 10
	}

	multiplier := int64(1)
	switch fileType {
	case "pdf", "docx", "pptx":
		multiplier = 3
	case "png", "jpg", "jpeg", "webp":
		multiplier = 2
	case "txt", "md", "csv":
		multiplier = 1
	default:
		multiplier = 2
	}

	cost := baseCost * multiplier

	if enableMultimodal {
		cost += 500
	}

	return cost
}
