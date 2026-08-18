; Outline queries for Go.
;
; Definitions only, and only at file scope. Two deliberate departures from the
; grammar's own queries/tags.scm:
;
;   1. tags.scm also emits @reference.call and @reference.type for every call
;      site and type mention. That exists for cross-file code navigation; here
;      it would bury the handful of declarations that matter under hundreds of
;      references, which is the opposite of what an outline is for.
;
;   2. Each pattern is anchored under (source_file). Go's const_declaration,
;      var_declaration and type_declaration also match inside function bodies,
;      so an unanchored pattern would list every local variable in the file.
;
; Doc comments are not matched here. tags.scm attaches them with #strip! and
; #set-adjacent!, directives the Rust tags crate implements and the Go bindings
; do not. codemap walks preceding sibling comments instead — see docStart().

(source_file (package_clause) @def.package)
(source_file (function_declaration) @def.func)
(source_file (method_declaration) @def.method)
(source_file (type_declaration) @def.type)
(source_file (const_declaration) @def.const)
(source_file (var_declaration) @def.var)
(source_file (import_declaration) @def.import)
