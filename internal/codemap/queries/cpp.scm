; Outline queries for C++.
;
; The C query's reasoning carries over — file scope reaches into every
; preprocessor conditional, so each pattern is repeated under those containers
; instead of left unanchored — and C++ adds two more containers of its own: a
; namespace's body and an extern "C" block are both a declaration_list.
;
; A template is captured at the declaration it wraps, which holds the name;
; the language's wrapperKind carries the range up over the `template <…>` line.
;
; Class members are listed when they are functions — definitions, and the
; declarations a header gives in place of them, constructors and operators
; included. Data members are not, for the reason TypeScript skips fields: the
; class's own range already covers them, and twenty one-line fields would bury
; its methods. A nested type is a (field_declaration) whose type is the
; specifier, so that is where those patterns look.
;
; Doc comments are not matched here — see docStart().

(preproc_include) @def.import

(translation_unit [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_ifdef [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_if [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_else [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_elif [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(preproc_elifdef [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(declaration_list [
  (function_definition) @def.func
  (declaration) @def.decl
  (type_definition) @def.type
  (alias_declaration) @def.type
  (namespace_definition) @def.namespace
  (class_specifier name: (_) body: (_)) @def.class
  (struct_specifier name: (_) body: (_)) @def.struct
  (union_specifier name: (_) body: (_)) @def.struct
  (enum_specifier name: (_) body: (_)) @def.enum
  (template_declaration [
    (function_definition) @def.func
    (declaration) @def.decl
    (alias_declaration) @def.type
    (class_specifier name: (_) body: (_)) @def.class
    (struct_specifier name: (_) body: (_)) @def.struct
  ])
  (preproc_def name: (_) value: (_)) @def.macro
  (preproc_function_def) @def.macro
])

(field_declaration_list [
  (function_definition) @def.method
  (declaration declarator: (function_declarator)) @def.method
  (field_declaration declarator: (function_declarator)) @def.method
  (field_declaration declarator: (pointer_declarator declarator: (function_declarator))) @def.method
  (field_declaration declarator: (reference_declarator (function_declarator))) @def.method
  (template_declaration [
    (function_definition) @def.method
    (declaration) @def.method
    (field_declaration) @def.method
  ])
  (field_declaration type: (class_specifier name: (_) body: (_)) @def.class)
  (field_declaration type: (struct_specifier name: (_) body: (_)) @def.struct)
  (field_declaration type: (union_specifier name: (_) body: (_)) @def.struct)
  (field_declaration type: (enum_specifier name: (_) body: (_)) @def.enum)
  (alias_declaration) @def.type
  (type_definition) @def.type
])
