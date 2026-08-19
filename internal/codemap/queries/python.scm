; Outline queries for Python.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript and PHP:
;
;   1. tags.scm also emits @reference.call for every call site. That exists for
;      cross-file navigation; here it would bury the declarations an outline is
;      for.
;
;   2. Each pattern is anchored under its container. A def inside a def is a
;      closure or a factory's inner helper, and listing those is the noise that
;      makes an outline worthless — the same reason the Go queries anchor under
;      (source_file).
;
; A decorated declaration is captured at the inner definition rather than at the
; decorated_definition wrapper, because the wrapper holds neither the name nor
; the signature — its first line is the decorator. buildSymbol widens the range
; back up over the decorators; see wrapperKind.
;
; Doc comments are not matched here either. Python documents from the inside,
; with a string literal at the top of the body — see docstringOf().

; --- top level -------------------------------------------------------------
(module (import_statement) @def.import)
(module (import_from_statement) @def.import)
(module (future_import_statement) @def.import)

(module (class_definition) @def.class)
(module (function_definition) @def.func)
(module (decorated_definition (class_definition) @def.class))
(module (decorated_definition (function_definition) @def.func))

(module (type_alias_statement) @def.type)

; A settings or constants module is nothing but module-level bindings. Skipping
; them the way class attributes are skipped would leave such a file outlining to
; an empty list, so they earn a line here — the same call TypeScript makes for a
; top-level lexical_declaration.
(module (expression_statement (assignment) @def.var))

; --- class members ---------------------------------------------------------
; Methods are where a class's code lives. Attributes are not: a dataclass with
; twenty annotated fields would bury its methods, and the class's own range
; already covers them. Same call as TypeScript's field_definition.
;
; Nested classes are kept — `class Meta` inside a model carries real
; configuration, and it reads as structure rather than noise.
(class_definition body: (block (function_definition) @def.method))
(class_definition body: (block (decorated_definition (function_definition) @def.method)))
(class_definition body: (block (class_definition) @def.class))
(class_definition body: (block (decorated_definition (class_definition) @def.class)))
