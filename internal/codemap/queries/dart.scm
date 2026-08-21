; Outline queries for Dart.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript, PHP, Python, Rust, Swift, Java and C#:
;
;   1. tags.scm also emits @reference.call for every call site. That exists for
;      cross-file navigation; here it would bury the declarations an outline is
;      for.
;
;   2. Each pattern is anchored under its container, so a function declared
;      inside another function's body stays out — the same reason the Go queries
;      anchor under (source_file).
;
; Dart states a function's body beside its signature rather than inside it: a
; top-level function parses as (function_signature) followed by a sibling
; (function_body), and a method as (method_signature (function_signature))
; followed by that same sibling. The capture is therefore the signature, and the
; range is carried down over the body afterwards — see trailingBodyKind.
;
; The inner signature is captured rather than the method_signature wrapping it,
; because the inner node is the one holding the name field. buildSymbol climbs
; back out to the wrapper to find the body, so both halves land on the symbol.

; --- directives -------------------------------------------------------------
; `library x;` is deliberately absent. It is deprecated in modern Dart, it
; imports nothing, and rendering it as an import block would misdescribe it.
(program (import_or_export) @def.import)
(program (part_directive) @def.import)

; --- top level --------------------------------------------------------------
(program (function_signature) @def.func)
(program (class_definition) @def.class)
(program (mixin_declaration) @def.mixin)
(program (extension_declaration) @def.extension)
(program (enum_declaration) @def.enum)
(program (type_alias) @def.type)

; Top-level `const`, `final` and `var` are absent, and not on the usual
; noise argument. The grammar does not wrap them in a node: `const int max = 3;`
; parses into three loose siblings of the program — a const_builtin, a
; type_identifier and a static_final_declaration_list — so there is nothing
; whose range covers the declaration to capture. Python's module-level bindings
; earn a line because that grammar gives them one node; here the outline would
; have to invent it.

; --- members ----------------------------------------------------------------
; class_body is the body of a class and of a mixin alike, so one set of patterns
; covers both. Dart has no nested types, so there is nothing to recurse into.
;
; A method with a body arrives inside a method_signature; an abstract one, which
; has no body to attach, arrives inside a plain declaration. Both spellings hold
; the same function_signature.
(class_body (method_signature (function_signature) @def.method))
(class_body (declaration (function_signature) @def.method))

; A getter and a setter are how Dart spells a property, and a type's properties
; are its public surface — the same call C# makes in listing its properties
; where Java's fields are skipped. They keep their own kinds because `get` and
; `set` on the same name are two declarations, and an outline that called both
; "method" would read as a duplicate.
(class_body (method_signature (getter_signature) @def.getter))
(class_body (method_signature (setter_signature) @def.setter))

; Constructors: the plain and named forms sit in a declaration, the const form
; alongside them, and a factory in a method_signature like a method.
(class_body (declaration (constructor_signature) @def.method))
(class_body (declaration (constant_constructor_signature) @def.method))
(class_body (method_signature (factory_constructor_signature) @def.method))

; Fields stay out, on the Java and C# reasoning: a class with twenty one-line
; fields would bury its methods, and the type's own range already covers them.

; --- extensions -------------------------------------------------------------
; An extension has a body of its own kind rather than a class_body.
(extension_body (method_signature (function_signature) @def.method))
(extension_body (method_signature (getter_signature) @def.getter))
(extension_body (method_signature (setter_signature) @def.setter))

; --- enums ------------------------------------------------------------------
; An enhanced enum states methods after its constants. The constants themselves
; stay out, as Java's do.
(enum_body (method_signature (function_signature) @def.method))
(enum_body (method_signature (getter_signature) @def.getter))
(enum_body (method_signature (setter_signature) @def.setter))
