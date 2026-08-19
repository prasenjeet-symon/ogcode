; Outline queries for Rust.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript, PHP and Python:
;
;   1. tags.scm also emits @reference.call and @reference.type for every call
;      site and type mention. That exists for cross-file navigation; here it
;      would bury the declarations an outline is for.
;
;   2. Each pattern is anchored under its container. Rust permits an item to be
;      declared inside a function body, and listing those is the noise that
;      makes an outline worthless — the same reason the Go queries anchor under
;      (source_file).
;
; Attributes are not matched here. Rust states #[derive(...)] beside the item
; rather than around it, so it is a preceding sibling; buildSymbol widens the
; range back up over it and looks for the doc comment above. See attrKind.

; --- top level -------------------------------------------------------------
(source_file (use_declaration) @def.import)
(source_file (extern_crate_declaration) @def.import)

(source_file (function_item) @def.func)
(source_file (struct_item) @def.struct)
(source_file (union_item) @def.struct)
(source_file (enum_item) @def.enum)
(source_file (trait_item) @def.trait)
(source_file (impl_item) @def.impl)
(source_file (mod_item) @def.mod)
(source_file (macro_definition) @def.macro)

(source_file (type_item) @def.type)
(source_file (const_item) @def.const)
(source_file (static_item) @def.var)

; --- inside an inline module ------------------------------------------------
; `mod tests { ... }` and its siblings hold real code, often most of a file's
; test surface. Without these patterns such a module would outline to a single
; `mod` line and nothing else.
(mod_item body: (declaration_list (use_declaration) @def.import))
(mod_item body: (declaration_list (function_item) @def.func))
(mod_item body: (declaration_list (struct_item) @def.struct))
(mod_item body: (declaration_list (union_item) @def.struct))
(mod_item body: (declaration_list (enum_item) @def.enum))
(mod_item body: (declaration_list (trait_item) @def.trait))
(mod_item body: (declaration_list (impl_item) @def.impl))
(mod_item body: (declaration_list (mod_item) @def.mod))
(mod_item body: (declaration_list (macro_definition) @def.macro))
(mod_item body: (declaration_list (type_item) @def.type))
(mod_item body: (declaration_list (const_item) @def.const))
(mod_item body: (declaration_list (static_item) @def.var))

; --- impl and trait members -------------------------------------------------
; Methods are where the code lives. Associated consts and types are not: they
; are the Rust equivalent of a field, and the block's own range already covers
; them. Same call as TypeScript's field_definition.
;
; A trait states each required method as a function_signature_item — a
; declaration with no body — and that signature is the whole point of the trait,
; so it earns a line alongside the defaulted methods beside it.
;
; These are anchored on impl_item and trait_item rather than on declaration_list
; alone, because a module body is a declaration_list too: a free function in a
; module is not a method.
(impl_item body: (declaration_list (function_item) @def.method))
(trait_item body: (declaration_list (function_item) @def.method))
(trait_item body: (declaration_list (function_signature_item) @def.method))
