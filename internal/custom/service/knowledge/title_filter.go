package knowledge

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// TitleFilterStatus describes whether a gated knowledge object can continue
// to the Wiki publication queue after title normalization.
type TitleFilterStatus string

const (
	TitleFilterPassed   TitleFilterStatus = "passed"
	TitleFilterRejected TitleFilterStatus = "rejected"
)

// TitleFilterDecision keeps the raw title and the deterministic decision
// together for audit output. It contains no Wiki or Graph identifiers.
type TitleFilterDecision struct {
	CandidateID   string               `json:"candidate_id"`
	Status        TitleFilterStatus    `json:"status"`
	OriginalTitle string               `json:"original_title"`
	CleanedTitle  string               `json:"cleaned_title,omitempty"`
	Object        *ClassifiedKnowledge `json:"object,omitempty"`
	Rejected      *RejectedProposition `json:"rejected,omitempty"`
	Reason        string               `json:"reason,omitempty"`
}

// TitleFilterBatch is the only P3-05 output that a later Wiki writer may
// consume. Rejected remains audit-only and is never part of Passed.
type TitleFilterBatch struct {
	Passed   []ClassifiedKnowledge `json:"passed"`
	Rejected []RejectedProposition `json:"rejected"`
}

var (
	titleHTMLTagPattern  = regexp.MustCompile(`(?s)<[^>]*>`)
	titleMarkdownLink    = regexp.MustCompile(`!?\[([^\]]+)\]\([^)]*\)`)
	titleWikiLink        = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	titleHeadingPrefix   = regexp.MustCompile(`^\s*#{1,6}\s*`)
	titleListPrefix      = regexp.MustCompile(`^\s*(?:[-+*•]\s+|(?:[0-9０-９]+|[一二三四五六七八九十百千万]+)\s*[.)、]\s*)`)
	titleChapterPrefix   = regexp.MustCompile(`(?i)^\s*第\s*[0-9０-９一二三四五六七八九十百千万]+\s*[章节部分篇]\s*[:：.．、)\-]?\s*`)
	titleEnglishChapter  = regexp.MustCompile(`(?i)^\s*(?:chapter|part)\s*[0-9０-９一二三四五六七八九十百千万]+\s*[:：.．、)\-]?\s*`)
	titleNumberPrefix    = regexp.MustCompile(`^\s*(?:[0-9０-９]+|[一二三四五六七八九十百千万]+)\s*[.．、:：)\-]\s*`)
	titleTemplatePrefix  = regexp.MustCompile(`(?i)^\s*(?:知识对象|知识条目|知识点|标题|名称|主题|对象|页面|内容|正文|实体|人物|机构|产品|技术|行业|地点|概念|方法论|方法|案例|洞察|术语|定义|问答|问题|结论|重点|title|name|topic|concept|methodology|method|case|insight|entity|knowledge(?:\s+(?:point|item|object))?)\s*[:：|]\s*`)
	titleEnglishQuestion = regexp.MustCompile(`(?i)^\s*(?:what|why|how|which|who|when|where|is|are|am|can|could|should|would|will|do|does|did|has|have|had)\b`)
)

var titleLeadingTransitions = []string{
	"接下来我们来看看",
	"接下来我们看",
	"下面我们来看看",
	"下面我们看",
	"今天我们聊聊",
	"今天我们来看",
	"首先我们来看",
	"然后我们来看",
	"最后我们来看",
	"由此可见",
	"综上所述",
	"总结一下",
	"本节介绍",
	"本章介绍",
	"下面介绍",
	"下面是",
	"首先来看",
	"然后来看",
	"最后来看",
	"我们来看看",
	"让我们看看",
	"认识一下",
	"了解一下",
	"介绍一下",
	"分析一下",
	"接下来",
	"下面",
	"本视频",
	"本期",
	"本章",
	"本节",
	"这一章",
	"这一节",
	"首先",
	"其次",
	"然后",
	"接着",
	"最后",
	"因此",
	"所以",
	"那么",
	"谈谈",
	"浅谈",
	"聊聊",
}

var titleQuestionPrefixes = []string{
	"想问一下",
	"请问",
	"什么是",
	"何谓",
	"如何理解",
	"如何看待",
	"如何判断",
	"如何进行",
	"如何使用",
	"如何",
	"为什么",
	"为何",
	"怎么理解",
	"怎么判断",
	"怎么做",
	"怎么用",
	"怎么样",
	"怎么",
	"是否",
	"能否",
	"可否",
	"哪个",
	"哪些",
	"有没有",
	"有何",
}

var titleQuestionSuffixes = []string{
	"是怎样的",
	"是什么",
	"的定义",
	"的含义",
	"的概念",
	"的意思",
	"怎么做",
	"如何做",
}

var titleTrailingDescriptors = []string{
	"的相关介绍",
	"的内容介绍",
	"相关介绍",
	"内容介绍",
	"的介绍",
	"的说明",
	"的概述",
}

// CleanTitle removes presentation wrappers and context-dependent phrasing.
// The returned reason is empty only when the result is a stable, searchable
// object name. It intentionally does not inspect source prose.
func CleanTitle(raw string) (string, string) {
	value := collapseTitleWhitespace(raw)
	if value == "" {
		return "", "empty_title"
	}

	value = stripTitleMarkup(value)
	value = collapseTitleWhitespace(value)
	if value == "" {
		return "", "empty_title"
	}
	contextDependentSource := isContextDependentTitle(value)
	englishQuestion := isEnglishQuestionTitle(value)

	chapterOnly := false
	for round := 0; round < 8; round++ {
		before := value
		var removed bool
		value, removed = stripTitleChapterOrListPrefix(value)
		chapterOnly = chapterOnly || removed
		value = stripTitleTemplatePrefix(value)
		value = strings.TrimSpace(value)
		if before == value {
			break
		}
	}
	if value == "" {
		if chapterOnly {
			return "", "chapter_marker"
		}
		return "", "empty_title"
	}

	transitionOnly := false
	questionDetected := false
	for round := 0; round < 5; round++ {
		before := value
		var removed bool
		value = stripTitleTemplatePrefix(value)
		value, removed = stripTitleLeadingTransition(value)
		transitionOnly = transitionOnly || removed
		englishQuestion = englishQuestion || isEnglishQuestionTitle(value)
		value, removed = stripTitleQuestionLanguage(value)
		questionDetected = questionDetected || removed
		value = stripTitleTemplatePrefix(value)
		value, removed = stripTitleLeadingTransition(value)
		transitionOnly = transitionOnly || removed
		value = stripTitleTrailingDescriptor(value)
		value = trimTitlePunctuation(value)
		value = collapseTitleWhitespace(value)
		if before == value {
			break
		}
	}

	if value == "" {
		switch {
		case questionDetected:
			return "", "question_title"
		case transitionOnly:
			return "", "transition_only"
		case chapterOnly:
			return "", "chapter_marker"
		default:
			return "", "empty_title"
		}
	}
	if strings.ContainsAny(value, "？?") || questionDetected && hasQuestionLanguage(value) {
		return "", "question_title"
	}
	if englishQuestion {
		return "", "question_title"
	}
	if strings.ContainsAny(value, "\r\n\t") {
		return "", "unstable_whitespace"
	}
	if hasResidualTitleMarkup(value) {
		return "", "template_marker"
	}
	if isContextDependentTitle(value) {
		return "", "context_dependent_title"
	}
	if isTemporaryTitle(value) {
		return "", "temporary_title"
	}
	if isGenericTitle(value) {
		if contextDependentSource {
			return "", "context_dependent_title"
		}
		return "", "generic_title"
	}
	if isOrdinaryTitle(value) {
		return "", "ordinary_word_meaning"
	}
	return value, ""
}

// FilterClassifiedKnowledge applies P3-05 to one P3-04 object. A pending or
// rejected object cannot be promoted into the next publication queue.
func FilterClassifiedKnowledge(object ClassifiedKnowledge) TitleFilterDecision {
	object = cloneTitleFilterObject(object)
	decision := TitleFilterDecision{
		CandidateID:   object.CandidateID,
		OriginalTitle: object.Title,
	}
	reject := func(reason string) TitleFilterDecision {
		decision.Status = TitleFilterRejected
		decision.Reason = reason
		decision.Rejected = &RejectedProposition{
			CandidateID: object.CandidateID,
			Title:       object.Title,
			PrimaryType: object.PrimaryType,
			Reason:      reason,
			EvidenceIDs: sortedEvidenceIDs(object.EvidenceIDs),
		}
		return decision
	}

	if err := ValidatePublishGate(object); err != nil {
		return reject("invalid_classified_knowledge: " + err.Error())
	}
	if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
		return reject("audit_status_not_passed")
	}

	cleaned, reason := CleanTitle(object.Title)
	if reason != "" {
		return reject(reason)
	}
	object.Title = cleaned
	decision.Status = TitleFilterPassed
	decision.CleanedTitle = cleaned
	decision.Object = &object
	return decision
}

// ApplyTitleFilter evaluates a set of already-gated objects and separates
// cleaned objects from audit-only rejections in stable candidate order.
func ApplyTitleFilter(objects []ClassifiedKnowledge) TitleFilterBatch {
	result := TitleFilterBatch{
		Passed:   make([]ClassifiedKnowledge, 0, len(objects)),
		Rejected: make([]RejectedProposition, 0),
	}
	for _, object := range objects {
		decision := FilterClassifiedKnowledge(object)
		if decision.Status == TitleFilterPassed && decision.Object != nil {
			result.Passed = append(result.Passed, *decision.Object)
		} else if decision.Rejected != nil {
			result.Rejected = append(result.Rejected, *decision.Rejected)
		}
	}
	sortTitleFilterBatch(&result)
	return result
}

// FilterPublishGateBatch carries P3-04 rejections forward and filters only
// P3-04 passed objects. This is the canonical P3-05 pipeline entry point.
func FilterPublishGateBatch(batch PublishGateBatch) TitleFilterBatch {
	result := ApplyTitleFilter(batch.Passed)
	result.Rejected = append(result.Rejected, batch.Rejected...)
	sortTitleFilterBatch(&result)
	return result
}

// TitleFilter is a concise pipeline alias.
func TitleFilter(objects []ClassifiedKnowledge) TitleFilterBatch {
	return ApplyTitleFilter(objects)
}

// DecodeTitleFilterBatch decodes the persisted P3-05 artifact with strict
// unknown-field protection, including protection against Wiki page IDs.
func DecodeTitleFilterBatch(data []byte) (TitleFilterBatch, error) {
	var value TitleFilterBatch
	if err := decodeStrict(data, &value); err != nil {
		return TitleFilterBatch{}, fmt.Errorf("decode title filter batch: %w", err)
	}
	sortTitleFilterBatch(&value)
	if err := value.Validate(); err != nil {
		return TitleFilterBatch{}, err
	}
	return value, nil
}

func (batch TitleFilterBatch) Validate() error {
	seen := make(map[string]struct{}, len(batch.Passed)+len(batch.Rejected))
	for _, object := range batch.Passed {
		if err := object.Validate(); err != nil {
			return fmt.Errorf("title-filter passed object %q: %w", object.CandidateID, err)
		}
		if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
			return fmt.Errorf("title-filter passed object %q must have audit_status passed", object.CandidateID)
		}
		if err := ValidatePublishGate(object); err != nil {
			return fmt.Errorf("title-filter passed object %q is not publishable: %w", object.CandidateID, err)
		}
		cleaned, reason := CleanTitle(object.Title)
		if reason != "" || cleaned != object.Title {
			return fmt.Errorf("title-filter passed object %q has non-canonical title", object.CandidateID)
		}
		if _, exists := seen[object.CandidateID]; exists {
			return fmt.Errorf("duplicate title-filter candidate_id %q", object.CandidateID)
		}
		seen[object.CandidateID] = struct{}{}
	}
	for _, rejected := range batch.Rejected {
		if strings.TrimSpace(rejected.CandidateID) == "" || strings.TrimSpace(rejected.Title) == "" || strings.TrimSpace(rejected.Reason) == "" {
			return fmt.Errorf("title-filter rejected proposition is incomplete")
		}
		if !IsKnowledgeType(rejected.PrimaryType) {
			return fmt.Errorf("title-filter rejected proposition has unsupported primary_type: %q", rejected.PrimaryType)
		}
		if err := validateEvidenceIDs(rejected.EvidenceIDs); err != nil {
			return fmt.Errorf("title-filter rejected proposition: %w", err)
		}
		if _, exists := seen[rejected.CandidateID]; exists {
			return fmt.Errorf("duplicate title-filter candidate_id %q", rejected.CandidateID)
		}
		seen[rejected.CandidateID] = struct{}{}
	}
	return nil
}

func sortTitleFilterBatch(batch *TitleFilterBatch) {
	if batch == nil {
		return
	}
	for index := range batch.Passed {
		batch.Passed[index] = cloneTitleFilterObject(batch.Passed[index])
	}
	for index := range batch.Rejected {
		batch.Rejected[index].EvidenceIDs = sortedEvidenceIDs(batch.Rejected[index].EvidenceIDs)
	}
	sort.SliceStable(batch.Passed, func(i, j int) bool {
		return batch.Passed[i].CandidateID < batch.Passed[j].CandidateID
	})
	sort.SliceStable(batch.Rejected, func(i, j int) bool {
		return batch.Rejected[i].CandidateID < batch.Rejected[j].CandidateID
	})
}

func stripTitleMarkup(value string) string {
	value = titleHTMLTagPattern.ReplaceAllString(value, "")
	value = titleWikiLink.ReplaceAllStringFunc(value, func(match string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(match, "[["), "]]")
		parts := strings.SplitN(inner, "|", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return strings.TrimSpace(parts[1])
		}
		return strings.TrimSpace(parts[0])
	})
	value = titleMarkdownLink.ReplaceAllString(value, "$1")
	value = strings.ReplaceAll(value, "```", "")
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	value = strings.ReplaceAll(value, "~~", "")
	value = titleHeadingPrefix.ReplaceAllString(value, "")
	value = strings.TrimSpace(strings.Trim(value, "*_~"))
	value = strings.Trim(value, "\"'“”‘’「」『』【】")
	return collapseTitleWhitespace(value)
}

func stripTitleChapterOrListPrefix(value string) (string, bool) {
	removed := false
	for round := 0; round < 8; round++ {
		before := value
		if titleHeadingPrefix.MatchString(value) {
			value = titleHeadingPrefix.ReplaceAllString(value, "")
			removed = true
		}
		if titleListPrefix.MatchString(value) {
			value = titleListPrefix.ReplaceAllString(value, "")
			removed = true
		}
		if titleChapterPrefix.MatchString(value) {
			value = titleChapterPrefix.ReplaceAllString(value, "")
			removed = true
		}
		if titleEnglishChapter.MatchString(value) {
			value = titleEnglishChapter.ReplaceAllString(value, "")
			removed = true
		}
		if titleNumberPrefix.MatchString(value) {
			value = titleNumberPrefix.ReplaceAllString(value, "")
			removed = true
		}
		value = strings.TrimSpace(value)
		if before == value {
			break
		}
	}
	return value, removed
}

func stripTitleTemplatePrefix(value string) string {
	for round := 0; round < 8; round++ {
		before := value
		value = titleTemplatePrefix.ReplaceAllString(value, "")
		value = strings.TrimSpace(value)
		if before == value {
			break
		}
	}
	return value
}

func stripTitleLeadingTransition(value string) (string, bool) {
	value = strings.TrimSpace(value)
	removed := false
	for round := 0; round < 8; round++ {
		before := value
		for _, prefix := range titleLeadingTransitions {
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
				value = strings.TrimSpace(value[len(prefix):])
				removed = true
				break
			}
		}
		value = strings.TrimLeft(value, " ：:，,、-")
		if before == value {
			return value, removed
		}
	}
	return value, removed
}

func stripTitleQuestionLanguage(value string) (string, bool) {
	value = strings.TrimSpace(value)
	removed := false
	for round := 0; round < 8; round++ {
		before := value
		for _, prefix := range titleQuestionPrefixes {
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
				value = strings.TrimSpace(value[len(prefix):])
				removed = true
				break
			}
		}
		for _, suffix := range titleQuestionSuffixes {
			if strings.HasSuffix(strings.ToLower(value), strings.ToLower(suffix)) {
				value = strings.TrimSpace(value[:len(value)-len(suffix)])
				removed = true
				break
			}
		}
		if strings.HasSuffix(value, "吗") || strings.HasSuffix(value, "呢") {
			value = strings.TrimSpace(value[:len(value)-len("吗")])
			removed = true
		}
		value = trimTitlePunctuation(value)
		if before == value {
			return value, removed
		}
	}
	return value, removed
}

func stripTitleTrailingDescriptor(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range titleTrailingDescriptors {
		if strings.HasSuffix(lower, strings.ToLower(suffix)) && len(value) > len(suffix) {
			return strings.TrimSpace(value[:len(value)-len(suffix)])
		}
	}
	return value
}

func cloneTitleFilterObject(value ClassifiedKnowledge) ClassifiedKnowledge {
	cloned := value
	if value.StructureFields != nil {
		cloned.StructureFields = make(map[string]string, len(value.StructureFields))
		for key, fieldValue := range value.StructureFields {
			cloned.StructureFields[key] = fieldValue
		}
	}
	if value.EvidenceIDs != nil {
		cloned.EvidenceIDs = append([]string(nil), value.EvidenceIDs...)
	}
	return cloned
}

func trimTitlePunctuation(value string) string {
	return strings.TrimFunc(strings.TrimSpace(value), func(r rune) bool {
		return strings.ContainsRune(" \t\r\n，,。！？?；;:：|、", r)
	})
}

func collapseTitleWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func hasResidualTitleMarkup(value string) bool {
	return strings.ContainsAny(value, "#[]`*~") || strings.Contains(value, "__")
}

func hasQuestionLanguage(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range titleQuestionPrefixes {
		if strings.Contains(lower, strings.ToLower(prefix)) {
			return true
		}
	}
	return strings.HasSuffix(value, "吗") || strings.HasSuffix(value, "呢")
}

func isEnglishQuestionTitle(value string) bool {
	return titleEnglishQuestion.MatchString(strings.TrimSpace(value))
}

func isContextDependentTitle(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, phrase := range []string{
		"本视频", "本期", "本章", "本节", "上文", "下文", "上述", "以下内容",
		"这里", "这个", "那个", "该视频", "该内容", "相关内容", "视频中",
		"我们今天", "今天", "当前内容", "本案例", "该案例", "本方法", "该方法",
		"这个方法", "本概念", "这个概念", "某个", "某种", "某公司",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isTemporaryTitle(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, phrase := range []string{
		"待定", "未命名", "无标题", "标题待定", "知识点待定", "暂定",
		"临时标题", "临时方案", "待补充", "待完善", "待确认", "未完成",
		"后续再看", "后续讨论", "后面再说", "稍后补充", "占位", "占位符",
		"placeholder", "todo", "tbd", "n/a",
	} {
		if lower == phrase || strings.HasPrefix(lower, phrase) {
			return true
		}
	}
	return false
}

func isGenericTitle(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"人物", "机构", "产品", "技术", "行业", "地点", "实体", "概念", "方法", "方法论", "案例", "洞察"} {
		for _, suffix := range []string{"介绍", "说明", "概述", "详解", "解析"} {
			if lower == strings.ToLower(prefix+suffix) {
				return true
			}
		}
	}
	switch lower {
	case "知识点", "知识对象", "对象", "标题", "名称", "主题", "内容", "正文",
		"问题", "结论", "重点", "案例", "方法", "方法论", "概念", "洞察",
		"实体", "人物", "机构", "产品", "技术", "行业", "地点", "信息", "说明",
		"介绍", "总结", "chapter", "part", "untitled":
		return true
	default:
		return false
	}
}

func isOrdinaryTitle(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "实", "知道", "普通词义", "实践含义", "字面意思", "释义", "一个字":
		return true
	}
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(compact)
	return len(runes) == 1 && unicode.Is(unicode.Han, runes[0])
}
