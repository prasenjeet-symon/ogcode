; Outline queries for Swift.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript, PHP, Python and Rust:
;
;   1. tags.scm also emits @reference.call for every call site. That exists for
;      cross-file navigation; here it would bury the declarations an outline is
;      for.
;
;   2. Each pattern is anchored under its container, so a declaration nested in
;      a function body stays out — the same reason the Go queries anchor under
;      (source_file).
;
; Attributes need no handling of their own. Swift parses `@objc public final`
; into a (modifiers) node inside the declaration, so the range and the signature
; both cover it already — where Python nests decorators above the declaration
; and Rust hangs attributes beside it.
;
; class_declaration is one node for class, struct, enum, actor and extension,
; told apart by an anonymous declaration_kind token. Matching that token splits
; them structurally, with no predicate to evaluate. They are kept apart rather
; than captured under one name because @def.type routes through the branch of
; signatureFor written for Go's grouped `type ( ... )` block, which slices the
; whole node — and so swallows the body of any type declared on one line.

; --- top level -------------------------------------------------------------
(source_file (import_declaration) @def.import)

(source_file (class_declaration declaration_kind: "class") @def.class)
(source_file (class_declaration declaration_kind: "struct") @def.struct)
(source_file (class_declaration declaration_kind: "enum") @def.enum)
(source_file (class_declaration declaration_kind: "actor") @def.actor)
(source_file (class_declaration declaration_kind: "extension") @def.extension)

(source_file (protocol_declaration) @def.protocol)
(source_file (function_declaration) @def.func)
(source_file (macro_declaration) @def.macro)

; A typealias and a top-level binding have no body, so the grouped-declaration
; branch is the right one for them.
(source_file (typealias_declaration) @def.type)
(source_file (property_declaration) @def.var)

; --- nested types -----------------------------------------------------------
; A type declared inside another is structure, not noise: `enum State` inside a
; view model is exactly the shape a reader is looking for. Same call as the
; nested class kept for Python.
(class_body (class_declaration declaration_kind: "class") @def.class)
(class_body (class_declaration declaration_kind: "struct") @def.struct)
(class_body (class_declaration declaration_kind: "enum") @def.enum)
(class_body (class_declaration declaration_kind: "actor") @def.actor)
(class_body (protocol_declaration) @def.protocol)
(class_body (typealias_declaration) @def.type)

; --- type members ----------------------------------------------------------
; Methods are where the code lives, and an initialiser is how the type is
; built, so both earn a line. Stored properties and enum cases do not: a model
; with twenty of them would bury its methods, and the type's own range already
; covers them. Same call as TypeScript's field_definition.
;
; class_body serves class, struct, actor and extension alike; enum_class_body
; is the enum equivalent.
(class_body (function_declaration) @def.method)
(class_body (init_declaration) @def.method)
(class_body (deinit_declaration) @def.method)
(class_body (subscript_declaration) @def.method)

(enum_class_body (function_declaration) @def.method)
(enum_class_body (init_declaration) @def.method)
(enum_class_body (subscript_declaration) @def.method)

; --- protocol requirements --------------------------------------------------
; A protocol body states requirements rather than implementation, and a
; property requirement is as much a part of the contract as a method — a Swift
; protocol is often mostly `var x: T { get }`. So every requirement is listed
; here, where a stored property on a concrete type is not.
(protocol_body (protocol_function_declaration) @def.method)
(protocol_body (protocol_property_declaration) @def.property)
(protocol_body (associatedtype_declaration) @def.type)
(protocol_body (init_declaration) @def.method)
