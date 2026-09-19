package types

import "strings"

// ImageClass is the coarse category a multimodal image is sorted into. The
// class is persisted alongside the image because it decides which per-image
// work runs next: text-bearing classes are worth OCR, decorative artwork is
// not.
type ImageClass string

const (
	// ImageClassDecorative is artwork that carries no information: dividers,
	// background fills, decorative icons, watermarks.
	ImageClassDecorative ImageClass = "decorative"
	// ImageClassLogo is a brand mark: a logo, badge, or emblem that identifies
	// an organisation or product. It is kept out of the decorative class on
	// purpose — a logo names an entity, and dropping it silently loses that.
	// When a label could go either way the classifier is told to prefer this
	// class: keeping a true decorative image costs one small caption, while
	// dropping a logo is information that cannot be recovered later.
	ImageClassLogo ImageClass = "logo"
	// ImageClassPhoto is a photograph or a picture of a physical thing.
	ImageClassPhoto ImageClass = "photo"
	// ImageClassTextScreenshot is a screenshot or scan dominated by body text.
	ImageClassTextScreenshot ImageClass = "text_screenshot"
	// ImageClassTableImage is a table rendered as an image.
	ImageClassTableImage ImageClass = "table_image"
	// ImageClassChart is a chart, graph, diagram, or infographic.
	ImageClassChart ImageClass = "chart"
	// ImageClassOther is both the explicit catch-all and the value an
	// unrecognised label degrades to.
	ImageClassOther ImageClass = "other"
)

// ImageClasses is the canonical class list, in the order the prompt presents it.
var ImageClasses = []ImageClass{
	ImageClassDecorative,
	ImageClassLogo,
	ImageClassPhoto,
	ImageClassTextScreenshot,
	ImageClassTableImage,
	ImageClassChart,
	ImageClassOther,
}

// ImageClassList renders the classes as the pipe-separated list the prompt
// shows the model.
func ImageClassList() string {
	parts := make([]string, 0, len(ImageClasses))
	for _, class := range ImageClasses {
		parts = append(parts, string(class))
	}
	return strings.Join(parts, "|")
}

// imageClassAliases maps the labels models actually emit onto the enum: the
// canonical names plus the near-synonyms seen in practice, so a reasonable
// answer is not discarded over wording.
var imageClassAliases = map[string]ImageClass{
	"decorative": ImageClassDecorative,
	"decoration": ImageClassDecorative,
	"deco":       ImageClassDecorative,
	"ornament":   ImageClassDecorative,
	"ornamental": ImageClassDecorative,
	"icon":       ImageClassDecorative,
	"divider":    ImageClassDecorative,
	"separator":  ImageClassDecorative,
	"background": ImageClassDecorative,
	"watermark":  ImageClassDecorative,

	// Logo keeps its own class. "decorative_logo" is listed verbatim because
	// the first-word fallback below would otherwise read it as decorative.
	"logo":            ImageClassLogo,
	"logos":           ImageClassLogo,
	"logotype":        ImageClassLogo,
	"brand_logo":      ImageClassLogo,
	"brand_mark":      ImageClassLogo,
	"trademark":       ImageClassLogo,
	"emblem":          ImageClassLogo,
	"decorative_logo": ImageClassLogo,

	"photo":                 ImageClassPhoto,
	"photograph":            ImageClassPhoto,
	"picture":               ImageClassPhoto,
	"photograph_of_a_scene": ImageClassPhoto,
	"product_shot":          ImageClassPhoto,

	"text_screenshot":     ImageClassTextScreenshot,
	"text":                ImageClassTextScreenshot,
	"texts":               ImageClassTextScreenshot,
	"screenshot":          ImageClassTextScreenshot,
	"screen_shot":         ImageClassTextScreenshot,
	"scan":                ImageClassTextScreenshot,
	"scanned":             ImageClassTextScreenshot,
	"document":            ImageClassTextScreenshot,
	"text_image":          ImageClassTextScreenshot,
	"document_screenshot": ImageClassTextScreenshot,
	"code_screenshot":     ImageClassTextScreenshot,
	"ui_screenshot":       ImageClassTextScreenshot,
	"paper":               ImageClassTextScreenshot,
	"page":                ImageClassTextScreenshot,

	"table_image":      ImageClassTableImage,
	"table":            ImageClassTableImage,
	"spreadsheet":      ImageClassTableImage,
	"sheet":            ImageClassTableImage,
	"table_screenshot": ImageClassTableImage,
	"grid":             ImageClassTableImage,

	"chart":       ImageClassChart,
	"graph":       ImageClassChart,
	"diagram":     ImageClassChart,
	"plot":        ImageClassChart,
	"figure":      ImageClassChart,
	"infographic": ImageClassChart,
	"flowchart":   ImageClassChart,
	"flow_chart":  ImageClassChart,
	"schematic":   ImageClassChart,

	"other":   ImageClassOther,
	"others":  ImageClassOther,
	"unknown": ImageClassOther,
	"misc":    ImageClassOther,
	"none":    ImageClassOther,
}

// normalizeImageClassLabel reduces a label to the lookup form shared by
// NormalizeImageClass and IsKnownImageClass: lower case, without the decorative
// punctuation models wrap answers in, and with separators spelled one way.
func normalizeImageClassLabel(raw string) string {
	label := strings.ToLower(strings.TrimSpace(raw))
	label = strings.Trim(label, "`'\"*_ .")
	label = strings.ReplaceAll(label, "-", "_")
	return strings.TrimSpace(strings.ReplaceAll(label, " ", "_"))
}

// NormalizeImageClass maps a model-provided label onto the enum. Labels models
// commonly produce instead of the exact word are folded onto their canonical
// class; anything unrecognised becomes ImageClassOther, so an unexpected label
// still results in usable behaviour instead of a value the rest of the pipeline
// would not recognise.
func NormalizeImageClass(raw string) ImageClass {
	label := normalizeImageClassLabel(raw)

	if class, ok := imageClassAliases[label]; ok {
		return class
	}
	// Models sometimes lead with extra words ("decorative logo", "photo of a
	// screw"). Retry on the first token so an answer that names the right class
	// is not thrown away over phrasing.
	if head, _, found := strings.Cut(label, "_"); found {
		if class, ok := imageClassAliases[head]; ok {
			return class
		}
	}
	return ImageClassOther
}

// IsKnownImageClass reports whether a label names a class the enum recognises,
// canonically or through one of the aliases NormalizeImageClass accepts.
//
// It exists for validating configuration rather than model replies.
// NormalizeImageClass folds anything it does not recognise onto ImageClassOther,
// which is right for a model answer but wrong for a rule an operator typed: a
// typo would silently become a rule about "other" images instead of being
// reported.
func IsKnownImageClass(raw string) bool {
	_, ok := imageClassAliases[normalizeImageClassLabel(raw)]
	return ok
}
