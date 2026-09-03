; Outline queries for CSS.
;
; One departure from the grammar's own queries/tags.scm, matching the calls
; already made for the other languages: tags.scm also emits each selector part
; and declaration separately, which for an outline would trade one readable
; rule per block for a dozen one-word lines.
;
; Patterns are unanchored — CSS nests rules inside the blocks of other rules,
; and anchoring under (stylesheet) alone would hide a media query's rules, the
; bulk of most real stylesheets. The captures arrive depth-first, so outer
; rules sort before the ones they contain and assignDepth indents them.
;
; Declarations are not matched here: a rule's body is the rule's range, and
; listing properties beside their rule is the field-level noise every other
; language in this package skips. Comments are matched nowhere too, for the
; same reason as in the other grammars — buildSymbol walks preceding comment
; siblings to attach them as the Doc of the rule below.

(rule_set) @def.rule
(media_statement) @def.media
(supports_statement) @def.supports
(keyframes_statement) @def.keyframes
(import_statement) @def.import
(at_rule) @def.at