package tui

type ResolvedImage struct {
	CacheKey string
	Columns  int
	Rows     int
	Label    string
	Protocol GraphicsProtocol
}

type ImageResolver func(source, alt string, maxColumns int) (ResolvedImage, bool)

type ImageDescriptor struct {
	Row      int
	RowSpan  int
	OffsetX  int
	Columns  int
	Source   string
	CacheKey string
	Alt      string
	Protocol GraphicsProtocol
}

type RichRender struct {
	Lines  []string
	Images []ImageDescriptor
}

type RichComponent interface {
	RenderRich(width int) RichRender
}

func RenderRichFrom(component Component, width int) RichRender {
	if rich, ok := component.(RichComponent); ok {
		return rich.RenderRich(width)
	}
	return RichRender{Lines: component.Render(width)}
}

type LayoutImage struct {
	Source     string
	CacheKey   string
	Alt        string
	Protocol   GraphicsProtocol
	X          int
	Y          int
	Rows       int
	Columns    int
	Generation uint64
}
