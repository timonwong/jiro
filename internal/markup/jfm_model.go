package markup

type sourceSpan struct {
	Start int
	End   int
}

type semanticDocument struct {
	Blocks []semanticBlock
}

const maxStructuredQuoteDepth = 64

type semanticBlock interface {
	semanticBlock()
}

type paragraphBlock struct {
	Span    sourceSpan
	Inlines []semanticInline
}

func (paragraphBlock) semanticBlock() {}

type headingBlock struct {
	Span    sourceSpan
	Level   int
	Inlines []semanticInline
}

func (headingBlock) semanticBlock() {}

type thematicBreakBlock struct {
	Span sourceSpan
}

func (thematicBreakBlock) semanticBlock() {}

type quoteBlock struct {
	Span   sourceSpan
	Blocks []semanticBlock
}

func (quoteBlock) semanticBlock() {}

type listItem struct {
	Span    sourceSpan
	Inlines []semanticInline
	Blocks  []semanticBlock
}

type listBlock struct {
	Span               sourceSpan
	Ordered            bool
	Items              []listItem
	RequiresFlattening bool
}

func (listBlock) semanticBlock() {}

type tableCell struct {
	Span    sourceSpan
	Inlines []semanticInline
}

type tableBlock struct {
	Span      sourceSpan
	Header    []tableCell
	Rows      [][]tableCell
	Raw       string
	Directive bool
}

func (tableBlock) semanticBlock() {}

type codeBlock struct {
	Span       sourceSpan
	Body       string
	Language   string
	Attributes []directiveAttribute
	Directive  bool
	NoFormat   bool
}

func (codeBlock) semanticBlock() {}

type panelBlock struct {
	Span       sourceSpan
	Attributes []directiveAttribute
	Blocks     []semanticBlock
}

func (panelBlock) semanticBlock() {}

type unsupportedMacroBlock struct {
	Span    sourceSpan
	Opening string
	Closing string
	Blocks  []semanticBlock
}

func (unsupportedMacroBlock) semanticBlock() {}

type literalBlock struct {
	Span sourceSpan
	Text string
}

func (literalBlock) semanticBlock() {}

type semanticInline interface {
	semanticInline()
}

type textInline struct {
	Span sourceSpan
	Text string
}

func (textInline) semanticInline() {}

type inlineStyle string

const (
	styleBold     inlineStyle = "bold"
	styleItalic   inlineStyle = "italic"
	styleStrike   inlineStyle = "strike"
	styleInserted inlineStyle = "inserted"
	styleSuper    inlineStyle = "superscript"
	styleSub      inlineStyle = "subscript"
	styleColor    inlineStyle = "color"
)

type styledInline struct {
	Span     sourceSpan
	Style    inlineStyle
	Value    string
	Children []semanticInline
}

func (styledInline) semanticInline() {}

type codeInline struct {
	Span sourceSpan
	Text string
}

func (codeInline) semanticInline() {}

type linkInline struct {
	Span   sourceSpan
	Label  []semanticInline
	Target string
	// Title is the advisory title both notations carry beside the target: the
	// third part of a Jira bracket body and the quoted title after a Markdown
	// destination. It is held as the characters a reader sees, which on the Jira
	// side means verbatim (decodeJiraLinkTitle).
	Title     string
	Unnamed   bool
	Directive bool
	Dangerous bool
}

func (linkInline) semanticInline() {}

type directiveAttribute struct {
	Span  sourceSpan
	Name  string
	Value string
	Bare  bool
}

type imageInline struct {
	Span       sourceSpan
	Alt        string
	Source     string
	Attributes []directiveAttribute
	Directive  bool
	Dangerous  bool
}

func (imageInline) semanticInline() {}

// emoticonInline is one Jira emoticon, written in JFM as `:emoticon[token]`.
// Token is the canonical spelling of a token the shared Jira inline grammar
// verifies, so `(*y)` and `(*)` are both carried as `(*)`.
type emoticonInline struct {
	Span  sourceSpan
	Token string
}

func (emoticonInline) semanticInline() {}

type hardBreakInline struct {
	Span sourceSpan
}

func (hardBreakInline) semanticInline() {}

type literalInline struct {
	Span sourceSpan
	Text string
}

func (literalInline) semanticInline() {}
