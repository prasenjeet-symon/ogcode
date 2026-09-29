; Outline queries for C.
;
; File scope in C is wider than the translation unit. A header wraps its whole
; body in an include guard, and a portable source file hides definitions behind
; #ifdef, so a file-scope declaration can sit inside any preprocessor
; conditional. Each pattern is repeated under those containers rather than left
; unanchored: an unanchored (declaration) would also match inside function
; bodies, which is where C declares its locals. A conditional can sit inside a
; function body too; codemap drops what it finds there (see localScopeKind).
;
; Function definitions need no anchor — C has no nested functions.
;
; An include guard's own `#define FOO_H` carries no value, so requiring one
; keeps it out of every header's map.
;
; Doc comments are not matched here — see docStart().

(preproc_include) @def.import
(function_definition) @def.func

(translation_unit [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_ifdef [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_if [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_else [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_elif [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_elifdef [
  (declaration) @def.decl
  (type_definition) @def.type
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])
