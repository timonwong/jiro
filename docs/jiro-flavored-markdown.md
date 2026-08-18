# Jiro Flavored Markdown Specification

JFM (Jiro Flavored Markdown) is a Markdown dialect that converts bidirectionally with Jira Markup. Write Issue descriptions and comments in Markdown; jiro converts them to Jira Markup for Jira storage. Read Jira content back as Markdown for local editing.

## 1. Status and conformance

This specification defines both JFM to Jira Markup conversion and Jira Markup to canonical JFM conversion.

The key words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and **MAY** are normative requirements.

A conforming converter MUST implement both conversion directions. Supporting only one direction is not JFM conformance.

JFM uses [CommonMark 0.31.2](https://spec.commonmark.org/0.31.2/) as its Markdown foundation and adopts the GitHub Flavored Markdown table, strikethrough, and [autolink-literal](https://github.github.com/gfm/#autolinks-extension-) extensions. JFM additionally recognizes bare `mailto:` URIs and adds controlled inline HTML and the directives defined here. Where this specification does not override CommonMark or an adopted extension, those rules apply.

JFM is jiro's complete Markdown-based format for bidirectional conversion with Jira Markup. Conversion preserves target-representable semantics but is not required to preserve source spelling or produce byte-for-byte identical text.

JFM is distinct from unrelated Jira-oriented Markdown dialects. A JFM document has no required front matter, magic header, or embedded format-version directive. New syntax SHOULD be additive; an older converter encountering an unknown directive applies the literal-fallback rules in this specification.

### Explicit boundaries

The following CommonMark-looking forms have explicit JFM boundaries:

- Task-list markers have no checkbox semantics and remain ordinary list text.
- Bare URLs, `www.` addresses, and email addresses use GFM autolink-literal semantics. Bare `mailto:` URIs are also autolinks.
- Reference-style links and images are accepted as input, but canonical JFM uses inline syntax or directives and does not emit reference definitions.
- Raw Jira Markup embedded in JFM has no special meaning.
- Malformed Markdown is not repaired before conversion.
- Jira citation notation is not defined by this version of JFM and follows the unsupported-notation rules.

## 2. Conversion model

Conversion is deterministic and best-effort. Every recognized construct belongs to one of four classes:

1. **Reversible**: all supported semantics are retained. Conversion produces no warning and the construct participates in canonical round-trip.
2. **Canonicalized**: semantics are retained but source spelling is normalized. Conversion produces no warning and the canonical form participates in round-trip.
3. **Lossy**: the base construct is recognized, but some information has no target representation. The converter MUST convert every representable part, MUST discard only the unrepresentable information, and MUST produce a warning.
4. **Literal fallback**: the converter cannot determine safe semantic boundaries. The complete construct MUST remain visible as escaped literal text and MUST produce a warning. Escaping means that target control characters in the preserved source are escaped so the text remains visible rather than becoming accidental formatting (see §14).

A converter MUST NOT use literal fallback merely because a recognized construct contains unsupported metadata. If the base structure can be converted safely, the unsupported information is lossy and the recognized structure continues through conversion.

Unknown or malformed directives, malformed controlled HTML, and constructs whose closing boundary cannot be determined safely use literal fallback. Unsupported source content MUST NOT cause surrounding recognized content to be dropped.

## 3. Canonical documents

Canonical JFM obeys these rules:

- Top-level blocks are separated by exactly one blank line.
- A document has no leading or trailing whitespace and no terminal newline.
- Structural line endings are LF. Literal code and noformat bodies preserve their authored internal whitespace and line endings.
- Soft line breaks in ordinary paragraphs become one space.
- Hard line breaks use a backslash followed by LF.
- Headings use ATX form without closing hashes.
- Unordered lists use `-`; ordered lists use `1.`.
- Nested list levels use four spaces of indentation.
- Strong emphasis uses `**`, emphasis uses `*`, and strikethrough uses `~~`.
- Jira-specific constructs use their canonical directive form when ordinary Markdown cannot preserve their semantics.
- Reference definitions, alternate list markers, setext headings, tilde fences, trailing-space hard breaks, and other accepted input spellings need not survive canonicalization.

Canonical Jira Markup is the deterministic Jira representation defined by the mappings in this specification. Source spelling, delimiter choice, list ordinal values, reference definitions, attribute case, and attribute order need not survive conversion.

Warning-free conversion MUST stabilize semantically:

- Jira Markup → JFM → Jira Markup → JFM produces the same canonical JFM after the first conversion.
- JFM → Jira Markup → JFM → Jira Markup produces the same canonical Jira Markup after the first conversion.

Inputs that produce warnings do not have a complete round-trip guarantee. Their canonicalized representable result MUST nevertheless stabilize after the lossy information has been removed.

## 4. Warnings

Every lossy conversion and literal fallback occurrence MUST produce one or more warnings. Warnings are separate from converted text and MUST NOT be embedded in JFM or Jira Markup.

A warning contains:

- `Line`: one-based source line.
- `Column`: one-based source column counted in Unicode code points.
- `Construct`: a stable lowercase kebab-case identifier.
- `Reason`: a human-readable explanation of the loss or fallback.

`Construct` is an open vocabulary. New identifiers may be added without changing the warning shape. Defined identifiers include `blockquote`, `code-block`, `directive`, `escape`, `heading`, `html`, `image`, `jira-macro`, `link`, `list`, `reference-definition`, `table`, and `utf-8`. Consumers MUST NOT treat this set as closed. `Reason` is explanatory prose and is not a machine-stable identifier.

Warnings remain in source occurrence order. Multiple warnings at the same position retain discovery order. Warnings are not merged or deduplicated, and every unsupported or malformed occurrence produces its own warning.

Position rules are:

- LF, CRLF, and bare CR each advance the line number once.
- A construct-level warning points to the first source character of the construct.
- An attribute warning points to the first character of the attribute name.
- An escape warning points to the triggering backslash.
- A reference-definition warning points to its opening `[`.
- A Jira macro warning points to its opening `{`.
- This version records only a start position, not an end position.

Invalid UTF-8 bytes are replaced individually with U+FFFD. Each replacement produces a warning at the invalid byte's source position. Successful conversion always produces valid UTF-8.

## 5. Escaping and text

Escaping is interpreted in the source notation before target escaping is applied. Decoded source delimiters MUST NOT become unintended target formatting.

- Jira backslash escapes decode Jira delimiters; doubled backslashes decode to one literal backslash.
- A Jira backslash before a non-delimiter remains literal, including Windows paths such as `C:\temp`.
- An escape that truncates or prevents closure of a recognized construct produces a warning; an otherwise unnecessary escape does not.
- Valid CommonMark named and numeric character references decode to their Unicode characters.
- Invalid character references remain visible text without a warning.
- Plain-text Jira effect delimiters (`*`, `_`, `-`, `+`, `^`, and `~`) are escaped in Jira output only when they participate in a complete formatting span. Unmatched effect delimiters and word-internal punctuation that Jira cannot tokenize as formatting remain unescaped. Jira structural delimiters and line controls retain their safety escaping.
- Plain-text characters that would start unintended Markdown formatting are escaped in JFM output.
- Directive attribute escapes are interpreted only inside directive attributes. Unknown escape sequences remain visible and produce warnings.

## 6. Headings and breaks

Jira `h1.` through `h6.` correspond to Markdown ATX headings `#` through `######`. JFM accepts CommonMark ATX and setext headings and emits canonical ATX headings. ATX input may use up to three leading spaces and an optional closing hash sequence. Canonical output begins at column one, has no closing hashes, and uses one space after a non-empty marker. An empty heading has no trailing space. `h0.`, `h7.`, and malformed Jira heading-like input remain visible and produce warnings.

Jira `----` corresponds to canonical JFM `---`. Any CommonMark thematic-break spelling converts to Jira `----`.

Jira `\\` corresponds to a Markdown hard break. JFM accepts both backslash hard breaks and two-or-more-trailing-space hard breaks and emits the backslash form canonically. Text preceding a hard-break marker MUST be retained.

<!-- jfm-spec-example: heading; direction: jira-to-jfm -->
Input:
~~~jira
h2. Release
~~~

Output:
~~~jfm
## Release
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: thematic-break; direction: jira-to-jfm -->
Input:
~~~jira
----
~~~

Output:
~~~jfm
---
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: hard-break; direction: jfm-to-jira -->
Input:
~~~jfm
first\
second
~~~

Output:
~~~jira
first\\
second
~~~

Warnings:
~~~json
[]
~~~

## 7. Inline formatting

The canonical mappings are:

| Jira Markup | JFM |
| --- | --- |
| `*bold*` | `**bold**` |
| `_italic_` | `*italic*` |
| `-strike-` | `~~strike~~` |
| `+inserted+` | `<ins>inserted</ins>` |
| `^superscript^` | `<sup>superscript</sup>` |
| `~subscript~` | `<sub>subscript</sub>` |
| `{color:red}text{color}` | `<font color="red">text</font>` |
| `{{code}}` | `` `code` `` |

Formatting nesting follows source nesting order. Controlled HTML tags are case-insensitive on input and lowercase in canonical JFM. `<ins>`, `<sup>`, and `<sub>` accept no attributes. `<font>` accepts exactly one `color` attribute; the value is passed through to Jira as-is, so any color format Jira accepts (named colors, hex such as `#ff0000`) is valid. Malformed, unclosed, mismatched, or attribute-bearing controlled HTML uses literal fallback.

JFM accepts `*` or `_` for emphasis and `**` or `__` for strong emphasis. Canonical output uses the spellings in the table above. A single span carrying both bold and italic uses `***...***`; distinct nested spans remain distinct and are not merged merely because delimiters touch. Jira effect delimiters must form a complete span. A hyphen cannot open a Jira strikethrough span between two ASCII alphanumeric characters, and a closing hyphen followed by an ASCII alphanumeric character does not close one. Ordinary hyphenated text such as `release-note` therefore remains text. JFM-to-Jira conversion escapes only those plain-text delimiters that would otherwise form a complete Jira effect span.

<!-- jfm-spec-example: jira-effect-token-boundaries; direction: jfm-to-jira -->
Input:
~~~jfm
-deleted-

this is not-deleted-

this is also:-deleted-

this is also&-deleted-

this is also$-deleted-
~~~

Output:
~~~jira
\-deleted\-

this is not-deleted-

this is also:\-deleted\-

this is also&\-deleted\-

this is also$\-deleted\-
~~~

Warnings:
~~~json
[]
~~~

Inline code uses a delimiter one backtick longer than the longest backtick run in its body, with a minimum length of one. Only CommonMark-required padding spaces may be added. Literal body content is otherwise preserved. Canonical Jira Markup encodes the Jira-active characters `&<>\{}[]|!*?_-+^~:` in an inline-code body as decimal character references. This prevents Jira from applying formatting, character references, automatic links, embedded objects, or a premature `}}` close while leaving unrelated punctuation readable. Jira-to-JFM conversion resolves character references once. Legacy Jira backslash escapes remain accepted, and delimiter-protection U+200B characters are removed only when they match the legacy inline-code safety patterns; unrelated U+200B characters remain content.

<!-- jfm-spec-example: inline-code-literal-punctuation; direction: jfm-to-jira -->
Input:
~~~jfm
`https://registry-mirror.alauda.io:60070/v2/ -literal-`
~~~

Output:
~~~jira
{{https&#58;//registry&#45;mirror.alauda.io&#58;60070/v2/ &#45;literal&#45;}}
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: inline-formatting; direction: jfm-to-jira -->
Input:
~~~jfm
**bold** *italic* ~~old~~ <ins>new</ins>
~~~

Output:
~~~jira
*bold* _italic_ -old- +new+
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: inline-formatting-reverse; direction: jira-to-jfm -->
Input:
~~~jira
*bold* _italic_ -old- +new+
~~~

Output:
~~~jfm
**bold** *italic* ~~old~~ <ins>new</ins>
~~~

Warnings:
~~~json
[]
~~~

## 8. Lists and block quotes

JFM accepts CommonMark unordered markers `-`, `*`, and `+` and ordered markers ending in `.` or `)`. Jira marker chains preserve the ordered or unordered type at every nesting level. Canonical JFM uses `-` and `1.` regardless of authored marker or ordinal. Authored ordered-list start values other than one are discarded with a warning because Jira Markup cannot retain them.

A list item with one inline paragraph followed by nested lists is reversible. When an item contains additional recognized blocks that Jira list markers cannot own:

1. The first paragraph remains the list item.
2. Remaining blocks are emitted as independent blocks at the nearest safely representable level, preserving source order.
3. Any nested-list tail after the interruption is flattened to a valid top-level list rather than emitted with orphan Jira markers.
4. The lost containment produces a warning. Formatting inside every emitted block is retained.

A hard break inside a list item cannot retain both the physical break and Jira marker ownership. The content before the break remains in the item; subsequent content is lifted to an independent paragraph and the loss of containment produces the same list warning.

A Jira list marker whose nesting level has no authored parent remains visible and produces a warning; a converter MUST NOT fabricate empty parent items. A top-level change between ordered and unordered markers starts a new canonical block.

JFM block quotes map to Jira `{quote}` containers. Jira `bq.` and `{quote}` are both accepted; canonical Jira output uses `{quote}`. CommonMark lazy-continuation forms are accepted. Paragraphs, lists, headings, tables, code blocks, panels, and nested quotes are supported inside a quote. Every quoted line in canonical JFM begins with `> `, while blank quoted lines contain only `>`.

Structured quote nesting MUST NOT exceed 64 levels. A deeper quote remains visible as literal content and produces a warning rather than causing a fatal conversion failure. Adjacent nested Jira `{quote}` close/open delimiters without a blank separator are syntactically ambiguous because opening and closing markers are identical; the containing quote therefore uses literal fallback with a warning.

<!-- jfm-spec-example: basic-list; direction: jfm-to-jira -->
Input:
~~~jfm
- alpha
- beta
~~~

Output:
~~~jira
* alpha
* beta
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: nested-list; direction: jfm-to-jira -->
Input:
~~~jfm
- outer
    - inner
~~~

Output:
~~~jira
* outer
** inner
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: ordered-start-loss; direction: jfm-to-jira -->
Input:
~~~jfm
7. seven
~~~

Output:
~~~jira
# seven
~~~

Warnings:
~~~json
[
  {"Line":1,"Column":1,"Construct":"list","Reason":"Jira Markup cannot retain an ordered list start other than 1"}
]
~~~

<!-- jfm-spec-example: complex-blockquote; direction: jfm-to-jira -->
Input:
~~~jfm
> first
>
> - one
> - two **bold**
~~~

Output:
~~~jira
{quote}
first

* one
* two *bold*
{quote}
~~~

Warnings:
~~~json
[]
~~~

## 9. Links and images

Ordinary Markdown links map to Jira `[label|target]`. An unnamed absolute URL uses `<https://example.com>` in canonical JFM; an unnamed `mailto:` target uses an email autolink. An unnamed document anchor uses `[#section](#section)`.

Bare URLs, `www.` addresses, and email addresses follow the GFM autolink-literal grammar, including its opening boundaries, trailing-punctuation removal, and balanced-parenthesis handling. JFM additionally recognizes a bare `mailto:` URI when the remainder is a valid GFM email address. Autolink recognition does not occur inside code spans or code blocks.

Bare `http://` and `https://` URLs map to unnamed Jira links `[target]`. A `www.` autolink keeps its authored label and uses the GFM-supplied `http://` target, producing `[label|target]`. Bare email addresses and bare `mailto:` URIs map to `[mailto:address]`. Canonical JFM uses an angle-bracket URI or email autolink for unnamed Jira links; a labeled `www.` link becomes an ordinary inline Markdown link.

Jira-only targets such as issue keys, attachments, and users use `:link[content]{target="..."}` when ordinary Markdown cannot represent the target safely. The `:link` directive requires exactly one quoted `target` attribute and accepts supported inline JFM in its content. It has no location for extra Jira parameters, so unknown or duplicate attributes make the complete directive malformed and invoke literal fallback.

Jira Markup has no link-title semantic. A Markdown link title is therefore discarded, while the label, target, and label formatting are converted normally. This is a lossy conversion and produces a warning.

Jira link labels cannot contain a physical hard break. A hard break inside a Markdown link label becomes one space, the link remains structural, and a warning records the discarded break semantic.

Standard Markdown images map to Jira image notation. Alternative text maps to Jira `alt`. Markdown image titles map to Jira `title` and are reversible.

An image with only source and optional alternative text uses standard Markdown syntax in canonical JFM; an image title or any additional Jira attribute uses `:image[alt]{src="..." ...}`. Canonical image attribute order is `src`, `thumbnail`, `align`, `border`, `bordercolor`, `hspace`, `vspace`, `width`, `height`, `title`. The `:image` directive requires exactly one quoted `src` attribute. `thumbnail` is presence-only; all other attributes require values. Unknown and duplicate attributes remain representable in Jira image parameter syntax, retain their source order after known attributes, and produce warnings.

Reference-style links and images are accepted as full, collapsed, or shortcut input and emitted canonically as inline syntax or directives. Used definitions are consumed. Unused or shadowed definitions remain literal and produce warnings. Unresolved references are ordinary visible CommonMark text and do not produce warnings.

Destinations using `javascript:`, `vbscript:`, or `data:` never become clickable Markdown links or images. Their target remains reversible through directives or Jira notation and produces a warning.

<!-- jfm-spec-example: link-directive; direction: jfm-to-jira -->
Input:
~~~jfm
:link[OPS-42]{target="OPS-42"}
~~~

Output:
~~~jira
[OPS-42|OPS-42]
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: link-directive-reverse; direction: jira-to-jfm -->
Input:
~~~jira
[OPS-42|OPS-42]
~~~

Output:
~~~jfm
:link[OPS-42]{target="OPS-42"}
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: autolink; direction: jfm-to-jira -->
Input:
~~~jfm
<https://example.com>
~~~

Output:
~~~jira
[https://example.com]
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: autolink-literals; direction: jfm-to-jira -->
Input:
~~~jfm
MR: https://gitlab-ce.alauda.cn/ait/cluster-cert-rotator/-/merge_requests/11

Site: www.example.com/docs.

Email: user@example.com

Mail: mailto:user@example.com
~~~

Output:
~~~jira
MR: [https://gitlab-ce.alauda.cn/ait/cluster-cert-rotator/-/merge_requests/11]

Site: [www.example.com/docs|http://www.example.com/docs].

Email: [mailto:user@example.com]

Mail: [mailto:user@example.com]
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: link-title-loss; direction: jfm-to-jira -->
Input:
~~~jfm
[Title](https://example.com "Read")
~~~

Output:
~~~jira
[Title|https://example.com]
~~~

Warnings:
~~~json
[
  {"Line":1,"Column":1,"Construct":"link","Reason":"Markdown link title was discarded because Jira Markup has no equivalent"}
]
~~~

<!-- jfm-spec-example: image-title; direction: jfm-to-jira -->
Input:
~~~jfm
![Diagram](diagram.png "Preview")
~~~

Output:
~~~jira
!diagram.png|alt=Diagram,title=Preview!
~~~

Warnings:
~~~json
[]
~~~

## 10. Tables

JFM accepts GFM pipe tables. Jira header cells use `||`; body cells use `|`. Table alignment is discarded with a warning because Jira Markup cannot retain it.

JFM-to-Jira conversion accepts representable inline content in a parsed GFM cell, including standard images. Such content MUST be converted rather than causing the table to become literal.

Canonical Jira-to-JFM conversion uses a GFM table only when every cell contains text, bold, italic, strikethrough, inline code, or standard Markdown links. Images, hard breaks, controlled HTML, directives, significant cell-edge whitespace, headerless tables, inconsistent row shapes, and other Jira-only cell semantics use the reversible `:::table` container instead. A GFM table cannot contain a physical-line-spanning hard break because the line ending terminates the row.

The `:::table` body contains canonical Jira table rows and is not parsed as Markdown. `:::table` accepts no attributes. Selecting this directive is canonicalization, not literal fallback, and does not produce a warning.

<!-- jfm-spec-example: table-image; direction: jfm-to-jira -->
Input:
~~~jfm
| A |
| --- |
| ![x](x.png) |
~~~

Output:
~~~jira
||A||
|!x.png|alt=x!|
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: table-image-canonical-form; direction: jira-to-jfm -->
Input:
~~~jira
||A||
|!x.png|alt=x!|
~~~

Output:
~~~jfm
:::table
||A||
|!x.png|alt=x!|
:::
~~~

Warnings:
~~~json
[]
~~~

## 11. Code and preformatted text

Jira `{{...}}` maps to inline code. Jira `{noformat}` maps to a fenced code block with an empty info string. An untyped Jira `{code}` maps canonically to an attribute-free `:::code` directive, while a Jira `{code}` carrying only a language maps to a language fence. Indented CommonMark code blocks map to Jira `{noformat}`. Fenced code blocks with a language map to Jira `{code}`.

A fenced code block info string has these JFM semantics:

- An empty info string produces `{noformat}`. This preserves the distinction between Jira noformat and Jira code blocks without adding another JFM directive.
- The first token is a language when it contains no `=` character.
- Language matching is case-insensitive for the following aliases: `js`, `jsx`, and `mjs` canonicalize to `javascript`; `sh`, `shell`, and `zsh` canonicalize to `bash`. These are the only built-in aliases; other single-token languages are retained as-is.
- Every token after the language is unsupported metadata. It is discarded and produces one warning for the code block.
- If the first token contains `=`, the block has no language; all info string content is discarded with a warning and the result is untyped `{code}`.
- Code body bytes and internal line endings are preserved.
- JFM accepts backtick and tilde fences of any valid CommonMark length. Canonical JFM uses a backtick fence one character longer than the longest backtick run in the body, with a minimum length of three.
- Code bodies are literal. Jira formatting notation and JFM directives inside a code body are never interpreted.

Jira code parameters are represented only by `:::code{attributes}`. Recognized attribute order is `language`, `title`, `theme`, `linenumbers`, `firstline`, `collapse`, `borderStyle`, `borderColor`, `borderWidth`, `bgColor`, `titleBGColor`, `titleColor`. `collapse` and `linenumbers` are boolean values. Other values are quoted strings.

The `:::code` directive may omit its attribute list to represent untyped Jira `{code}`. Its fence follows the general safe-container rule: its colon run is longer than any standalone colon-fence line in the literal code body. Code length never creates an implicit `collapse=true`; collapse is enabled only by an explicit directive attribute.

<!-- jfm-spec-example: fenced-code-metadata-loss; direction: jfm-to-jira -->
Input:
~~~jfm
```JavaScript title=
code
```
~~~

Output:
~~~jira
{code:language=javascript}
code
{code}
~~~

Warnings:
~~~json
[
  {"Line":1,"Column":1,"Construct":"code-block","Reason":"fenced code info string metadata was discarded"}
]
~~~

<!-- jfm-spec-example: parameterized-code; direction: jfm-to-jira -->
Input:
~~~jfm
:::code{language="javascript" title="Example" collapse=true}
const x = 1
:::
~~~

Output:
~~~jira
{code:language=javascript|title=Example|collapse=true}
const x = 1
{code}
~~~

Warnings:
~~~json
[]
~~~

<!-- jfm-spec-example: untyped-code-canonical-form; direction: jira-to-jfm -->
Input:
~~~jira
{code}
plain
{code}
~~~

Output:
~~~jfm
:::code
plain
:::
~~~

Warnings:
~~~json
[]
~~~

## 12. Directive syntax

Inline directives have the form `:name[content]{attributes}`. Container directives use a colon fence, a name, optional attributes, a body, and a closing fence of equal length. A container fence contains at least three colons and MUST be longer than any nested colon fence in its body.

The defined directives are `:link`, `:image`, `:::code`, `:::table`, and `:::panel`.

### Naming

- Directive names match `[A-Za-z][A-Za-z0-9-]*`.
- Attribute names match `[A-Za-z][A-Za-z0-9_-]*`.
- JFM does not support `.class` or `#id` shorthand.
- Names are case-insensitive on input and use canonical case on output.

### Whitespace and layout

- Input permits spaces or tabs between attributes and around `=`, but an attribute list never spans a physical line.
- Inline directives permit no whitespace between the name, content brackets, and attribute braces.
- Container opening fences permit only whitespace after their optional attribute list.
- Canonical attributes use one ASCII space between attributes and no whitespace around `=`.

### Attribute values

- String values are double-quoted and use JSON-style escapes for quote, backslash, newline, carriage return, and tab.
- Boolean values are lowercase unquoted `true` or `false`. Known boolean values are case-insensitive on input and lowercase canonically. An invalid boolean remains a quoted visible value and produces a warning.
- Presence-only flags, such as image `thumbnail`, have no value.
- Every non-boolean value is a quoted string, including empty and numeric-looking values.
- Attribute values never span source lines. Unknown escape sequences remain visible and produce warnings.

### Attribute ordering and duplicates

- Known attributes use directive-defined canonical order.
- Unknown known-directive attributes follow known attributes and retain their relative source order.
- Duplicate attributes retain source order rather than using first-wins or last-wins behavior.
- Unknown and duplicate attributes are retained only when Jira has a parameter location that can represent them; each produces a warning.

### Error handling

- A missing required attribute or an unsafe identifier makes the complete directive malformed and invokes literal fallback.
- A Jira parameter name that cannot be represented by the attribute-name grammar leaves the complete Jira construct visible and produces a warning; it is never renamed.

### Inline directive content

- Inline directive content never spans a physical line. `]` and backslash are escaped as `\]` and `\\`.
- `:link` content is inline JFM; `:image` content is plain alternative text. A content-model violation invokes literal fallback with a warning.

## 13. Panels

Jira `{panel}` maps to `:::panel`. A panel recursively contains every JFM block and inline construct, including nested panels. Canonical panel attribute order is `title`, `borderStyle`, `borderColor`, `borderWidth`, `bgColor`, `titleBGColor`, `titleColor`.

Nested container directives use the shortest safe colon-fence length. Panel attributes retain Jira names and do not use unrelated panel type vocabularies.

<!-- jfm-spec-example: panel; direction: jira-to-jfm -->
Input:
~~~jira
{panel:title=Data}
h1. Report
{panel}
~~~

Output:
~~~jfm
:::panel{title="Data"}
# Report
:::
~~~

Warnings:
~~~json
[]
~~~

## 14. Unsupported and malformed input

Unknown Jira macros retain their opening and closing notation while recognized body content continues through conversion. A warning identifies the unknown macro and the fact that its render mode is unknown.

Unknown JFM directives and malformed supported directives use literal fallback because their semantic boundary or required data is not trustworthy. Unsupported or malformed controlled HTML likewise remains literal. Literal fallback escapes target control characters so the preserved source remains visible rather than becoming accidental formatting.

Source-content problems are never fatal. A conforming conversion produces the best complete result it can, with warnings identifying every loss or fallback occurrence.
