; Outline queries for HTML.
;
; One departure from the grammar's own queries/tags.scm, matching the calls
; already made for the other languages:
;
;   1. tags.scm emits every element plus @reference anchors for links, images
;      and scripts. That exists for cross-file navigation; here it would bury
;      the structural landmarks an outline is for. An element is captured only
;      when it carries an id or a class — the two attributes an author puts on
;      a region worth jumping to.
;
; The predicate is text-matched rather than structured because attribute names
; and values are separate leaves with no field between them, and because a
; failed predicate drops the whole match — which is what keeps a <div> with
; neither attribute from producing a capture.
;
; script and style elements are captured unconditionally. Their body parses as
; a raw_text leaf, so the code inside them is invisible to every other query
; in this package; without an entry a page's only JavaScript would outline to
; nothing at all.
;
; Comments are not matched here: buildSymbol walks preceding comment siblings
; for doc attachment (commentKinds), so <!-- nav --> above <nav id="..."> lands
; in the nav element's Doc, and a standalone comment needs no outline line.

((element
  (start_tag (attribute (attribute_name) @attr))) @def.element
  (#any-of? @attr "id" "class"))

((element
  (self_closing_tag (attribute (attribute_name) @attr))) @def.element
  (#any-of? @attr "id" "class"))

(script_element) @def.script
(style_element) @def.style