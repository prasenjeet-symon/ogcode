; Every HCL block is a declaration: a type word, optional quoted labels, and a
; body. Nesting falls out of the ranges, so the pattern is left unanchored —
; `lifecycle` inside a `resource` is captured and indented like any other block.
(block) @def.block
