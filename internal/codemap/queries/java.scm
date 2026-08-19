; Outline queries for Java.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript, PHP, Python, Rust and Swift:
;
;   1. tags.scm also emits @reference.call and @reference.class for every call
;      site and type mention. That exists for cross-file navigation; here it
;      would bury the declarations an outline is for.
;
;   2. Each pattern is anchored under its container, so a class declared inside
;      a method body stays out — the same reason the Go queries anchor under
;      (source_file).
;
; Annotations need no handling of their own. Java parses `@Entity public final`
; into a (modifiers) node inside the declaration, so the range and the signature
; both cover it already — as in Swift, and unlike Python's decorators or Rust's
; attributes, which sit outside the node.

; --- top level -------------------------------------------------------------
(program (package_declaration) @def.package)
(program (import_declaration) @def.import)

(program (class_declaration) @def.class)
(program (interface_declaration) @def.interface)
(program (enum_declaration) @def.enum)
(program (record_declaration) @def.record)
(program (annotation_type_declaration) @def.annotation)

; --- nested types -----------------------------------------------------------
; A static nested class or a Builder is structure, not noise, and Java leans on
; the shape heavily. Same call as the nested class kept for Python and Swift.
;
; class_body serves classes and records alike.
(class_body (class_declaration) @def.class)
(class_body (interface_declaration) @def.interface)
(class_body (enum_declaration) @def.enum)
(class_body (record_declaration) @def.record)

(interface_body (class_declaration) @def.class)
(interface_body (interface_declaration) @def.interface)
(interface_body (enum_declaration) @def.enum)
(interface_body (record_declaration) @def.record)

; --- members ---------------------------------------------------------------
; Methods are where the code lives, and a constructor is how the type is built,
; so both earn a line. Fields and enum constants do not: a class with twenty
; one-line fields would bury its methods, and the type's own range already
; covers them. Same call as TypeScript's field_definition.
;
; A static initialiser is skipped as well — it binds no name, so a line for it
; would read as a blank entry.
(class_body (method_declaration) @def.method)
(class_body (constructor_declaration) @def.method)
(class_body (compact_constructor_declaration) @def.method)

; An interface method carries no body, and that signature is the point of the
; interface, so it is listed beside the default methods next to it.
(interface_body (method_declaration) @def.method)

; An enum states its methods after the constants, inside a nested
; enum_body_declarations rather than directly in the body.
(enum_body_declarations (method_declaration) @def.method)
(enum_body_declarations (constructor_declaration) @def.method)
(enum_body_declarations (class_declaration) @def.class)

; An annotation's elements are its contract, the same way an interface's methods
; are.
(annotation_type_body (annotation_type_element_declaration) @def.method)
