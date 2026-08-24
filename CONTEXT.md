# jiro Jira CLI

jiro provides a scriptable command-line interface for working with Jira Data Center and Server. Its language distinguishes Jira-owned identifiers and markup from the friendlier aliases and input formats accepted by the CLI.

## Language

**Jira Instance**:
A Jira Data Center or Server deployment addressed by one base URL.
_Avoid_: Site, server, host

**Profile**:
A named set of connection and authentication settings for one Jira Instance. A Profile may own one persisted Credential.
_Avoid_: Account, environment

**Credential**:
A secret that proves a Profile may act as a Jira user, represented by either a Basic Auth password or a Personal Access Token.
_Avoid_: Session, account

**Login**:
The local operation that verifies a fresh Credential with a Jira Instance and associates it with a Profile. It does not create a persistent Jira server session.
_Avoid_: Session creation, browser login

**Logout**:
The local removal of a Profile's persisted Credential. It does not revoke a Jira Personal Access Token or unset credentials supplied by the environment.
_Avoid_: Token revocation, session expiry

**Issue Key**:
The human-readable Jira identifier for an issue, such as `PROJ-123`.
_Avoid_: Ticket ID, issue ID

**Issue Clone**:
A newly created Issue derived from one source Issue through `issue clone`. An Issue Clone is independent from its source, keeps the source Project and Issue Type, and may retain a directional `Cloners` Issue Link and one active Sprint Membership.
_Avoid_: Copy, duplicate ticket

**Custom Field ID**:
The Jira-owned, instance-specific identifier for a custom field, such as `customfield_10006`.
_Avoid_: Custom field key

**Custom Field Alias**:
A jiro-friendly slug derived from a custom field's display name, such as `story-points`.
_Avoid_: Custom Field ID, field name

**Source Custom Field Visibility**:
The set of Custom Fields visible to the authenticated Principal on a source Issue. It is distinct from the target Create screen's writable Custom Fields.
_Avoid_: Full Custom Field Set, Create Field

**Field Selector**:
A user-facing reference to an Issue field. jiro resolves it without requiring the caller to distinguish a Jira field ID, Custom Field ID, or Custom Field Alias.
_Avoid_: Field name, field reference

**Field Selection**:
An ordered record of each requested Field Selector and the exact Jira selector produced from it.
_Avoid_: Field map, selected fields

**Principal**:
The Jira user identity authenticated for one operation, independent of the local Profile and Credential used to prove it.
_Avoid_: Profile, Credential, account

**Field Metadata Snapshot**:
A time-bounded copy of the fields visible to one Principal on one Jira Instance. It is disposable and is not the source of truth.
_Avoid_: Field configuration, field registry

**Jira Markup**:
Jira's wiki-style text notation accepted directly by Jira Data Center and Server rich-text fields.
_Avoid_: Jira Markdown, wiki Markdown

**Jiro Flavored Markdown**:
jiro's sole Markdown-based format for representing Jira Markup bidirectionally, including jiro-owned extensions for Jira-specific semantics. Warning-free constructs round-trip canonically; recognized lossy constructs retain every target-representable semantic and report discarded information separately.
_Avoid_: Markdown Input, Markdown Projection, Jira Flavored Markdown, Jira export

**Jira Emoticon**:
An inline semantic produced when Jira interprets a supported emoticon token such as `(x)` or `:)` as an icon. It is distinct from the same characters authored as literal text in Jiro Flavored Markdown.
_Avoid_: Emoji, smiley text

**Jira Emoticon Directive**:
The Jiro Flavored Markdown inline directive `:emoticon[...]` whose content is one supported Jira Emoticon token. It is the canonical JFM representation of an icon semantic, not a request to preserve arbitrary renderer HTML.
_Avoid_: Emoji directive, icon HTML

**Canonical Jira Markup**:
The single Jira Markup form jiro produces for a given Jiro Flavored Markdown document. It is jiro's output, not a stable representation of what a Jira Instance stores.
_Avoid_: Escaped markup, wire format

**Jira Line Start**:
A position where Jira begins reading block syntax: the first character of a physical line after the spaces and tabs it skips, the character after a forced newline, the start of every table cell, and the start of every list item's content. A Marker Run, `h1.` through `h6.`, `bq.`, and `----` open a block only there, and a list item's content start is the one that reads no Marker Run: `* * y` is the item text `* y` while `* h1. y` is a heading inside the item.
_Avoid_: Line beginning, column zero, block start

**Marker Run**:
The unbroken run of `*`, `-`, and `#` that opens a Jira list item at a Jira Line Start when a space or a tab follows it. Its length is the item's nesting level and its last character types that level: a round bullet, a square bullet, or an ordered item. Jiro Flavored Markdown spells one bullet, so a round and a square Marker Run both write as `-`.
_Avoid_: Marker chain, bullet, list prefix

**Text Effect**:
An inline style that Jira Markup expresses with a matched pair of effect delimiters, such as bold, italic, strikethrough, inserted, superscript, subscript, and citation.
_Avoid_: Style, formatting, emphasis

**Effect Delimiter**:
A character that opens or closes one Text Effect when Jira's word-boundary rules allow it; the same character elsewhere is literal text. It has a brace form, `{*}` or `{??}`, which waives the word-boundary rule on the delimiter's outer side and pairs with the bare form.
_Avoid_: Special character, markup character

**Monospace Span**:
The Jira Markup inline construct written as `{{...}}`. It corresponds one-to-one with Jiro Flavored Markdown inline code, but Jira still applies Text Effects, links, and other inline rules inside it.
_Avoid_: Code span, inline code (when referring to the Jira side)

**Line Domain**:
The extent Jira reads a forced newline in: one physical line of a block, or one table cell. A `\\` pair is a forced newline only as the last backslash run of its whitespace-separated token inside that extent, so the same pair breaks in one line and stays two characters in another. A link's visible text has no Line Domain and never breaks.
_Avoid_: Line, paragraph, inline run

**Delimited Value**:
A value Jira Markup reads between its own delimiters: a link target, an image source, an image parameter value, or a macro parameter value. Each context consumes backslashes by its own rule and splits on its separator whether or not a backslash precedes it, so a character reference is what carries a delimiter into one.
_Avoid_: Attribute value, escaped value

**Board**:
A Jira Software planning view whose Sprint endpoint defines one queryable relationship between that Board and each returned Sprint.
_Avoid_: Project, Sprint container

**Sprint**:
A Jira Software planning interval that groups Issues for a time-bounded body of work.
_Avoid_: Iteration, milestone

**Sprint Membership**:
A relationship indicating that an Issue belongs or previously belonged to one Sprint. It is distinct from the Board-to-Sprint relationship returned by Sprint discovery.
_Avoid_: Sprint assignment, Board Sprint

**Issue Link**:
A directional relationship between two Jira Issues, identified by Jira so it can be listed and removed deterministically.
_Avoid_: Web Link, dependency

**Link Type**:
A Jira-owned definition that names an Issue Link and its inward and outward relationship descriptions.
_Avoid_: Relationship type, link name

**Bulk Operation**:
One requested action applied by jiro to every Issue selected by JQL, with an ordered outcome recorded for each Issue.
_Avoid_: Transaction, batch endpoint

**API Request**:
One authenticated HTTP request sent by `jiro api` to a relative endpoint of the selected Jira Instance. Its response is Jira-owned wire data outside jiro's normalized schema.
_Avoid_: Typed command, normalized response
