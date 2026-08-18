; Outline queries for TypeScript and TSX.
;
; Shared by both grammars: TSX is TypeScript plus JSX, and nothing captured here
; is JSX-specific, so one query file compiles against both languages.
;
; Every top-level form appears twice — bare, and wrapped in `export_statement`.
; `export function f() {}` parses as (export_statement (function_declaration)),
; so a pattern anchored only under (program) would match the export wrapper and
; miss the declaration inside it. Most declarations in a TypeScript codebase are
; exported, so omitting the second form would lose most of the file.
;
; The wrapped patterns capture the inner declaration, not the export_statement.
; The two start on the same line, so line ranges are unaffected, and docStart
; climbs to the outermost ancestor on that line to find a doc comment attached
; to the `export` (see docAnchor).

; --- top level -------------------------------------------------------------
(program (function_declaration) @def.func)
(program (generator_function_declaration) @def.func)
(program (class_declaration) @def.class)
(program (abstract_class_declaration) @def.class)
(program (interface_declaration) @def.interface)
(program (type_alias_declaration) @def.type)
(program (enum_declaration) @def.enum)
(program (lexical_declaration) @def.const)
(program (variable_declaration) @def.var)
(program (internal_module) @def.namespace)
(program (import_statement) @def.import)

; --- the same, exported ----------------------------------------------------
(program (export_statement (function_declaration) @def.func))
(program (export_statement (generator_function_declaration) @def.func))
(program (export_statement (class_declaration) @def.class))
(program (export_statement (abstract_class_declaration) @def.class))
(program (export_statement (interface_declaration) @def.interface))
(program (export_statement (type_alias_declaration) @def.type))
(program (export_statement (enum_declaration) @def.enum))
(program (export_statement (lexical_declaration) @def.const))
(program (export_statement (variable_declaration) @def.var))
(program (export_statement (internal_module) @def.namespace))

; --- class members ---------------------------------------------------------
; Methods are where a class's code actually lives, so they earn a line each.
; Field definitions do not: a class with twenty one-line properties would bury
; its methods, and the class's own range already covers them.
(class_body (method_definition) @def.method)

; --- functions nested inside functions --------------------------------------
; A SolidJS or React component is one top-level arrow function holding the whole
; module: mapping it to a single 900-line range tells a reader nothing they did
; not already know. These patterns reach inside it for the handlers and helpers
; that make up its structure.
;
; Only function-valued declarations qualify. A bare `const n = 1` is a local, and
; listing locals is the noise that makes an outline worthless — the same reason
; the Go queries anchor under (source_file).
(statement_block (function_declaration) @def.func)
(statement_block (lexical_declaration (variable_declarator value: (arrow_function))) @def.func)
(statement_block (lexical_declaration (variable_declarator value: (function_expression))) @def.func)
