; Outline queries for PHP.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go and TypeScript:
;
;   1. tags.scm also emits @reference.call for every call site and object
;      creation. That exists for cross-file navigation; here it would bury the
;      handful of declarations that matter under hundreds of references.
;
;   2. Each pattern is anchored under its container. PHP permits a class or a
;      function to be declared inside a function body or a conditional, so an
;      unanchored pattern would list declarations that are not part of the
;      file's structure — the same reason the Go queries anchor under
;      (source_file).
;
; Doc comments are not matched here. PHP's `/** */` docblocks are plain
; (comment) nodes; codemap walks preceding siblings for them — see docStart().

; --- top level -------------------------------------------------------------
(program (namespace_definition) @def.namespace)
(program (namespace_use_declaration) @def.import)
(program (function_definition) @def.func)
(program (class_declaration) @def.class)
(program (interface_declaration) @def.interface)
(program (trait_declaration) @def.trait)
(program (enum_declaration) @def.enum)
(program (const_declaration) @def.const)

; --- inside a braced namespace ---------------------------------------------
; `namespace App { ... }` nests every declaration one level down. It is the
; rarer of the two namespace forms, but a file using it would otherwise outline
; to a single `namespace` line and nothing else.
(namespace_definition (compound_statement (namespace_use_declaration) @def.import))
(namespace_definition (compound_statement (function_definition) @def.func))
(namespace_definition (compound_statement (class_declaration) @def.class))
(namespace_definition (compound_statement (interface_declaration) @def.interface))
(namespace_definition (compound_statement (trait_declaration) @def.trait))
(namespace_definition (compound_statement (enum_declaration) @def.enum))
(namespace_definition (compound_statement (const_declaration) @def.const))

; --- members ---------------------------------------------------------------
; Methods are where a class's code actually lives, so they earn a line each.
; Properties, class constants and enum cases do not: a class with twenty
; one-line properties would bury its methods, and the class's own range already
; covers them. Same call as TypeScript's field_definition.
;
; declaration_list is the body of a class, an interface and a trait alike;
; enum_declaration_list is the enum equivalent. Both are matched so a method
; is listed wherever it is declared.
(declaration_list (method_declaration) @def.method)
(enum_declaration_list (method_declaration) @def.method)
