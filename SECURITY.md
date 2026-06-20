# Security Policy

## Reporting a Vulnerability

Please do not open a public issue for suspected vulnerabilities. Use GitHub private vulnerability reporting on this repository if it is available. If private reporting is not enabled, contact the maintainer through the GitHub profile associated with this repository.

Include:

- affected SnapZip version or commit
- operating system and Go version
- steps to reproduce
- expected impact
- whether a generated `memory.db` or local indexed codebase is involved

## Scope

SnapZip is local-first software. The main security boundary is local project data: indexed snippets, feedback memory, generated sketches, benchmark outputs, and release artifacts should stay under user control unless the user explicitly shares them.

Supported security work includes:

- vulnerabilities that expose or corrupt local `memory.db` contents
- unsafe handling of indexed source files
- release packaging or dependency vulnerabilities
- command-line behavior that unexpectedly writes outside requested paths

Out of scope:

- vulnerabilities in third-party compilers or interpreters invoked by the user
- malicious code intentionally indexed from an untrusted codebase
- benchmark results or generated files created outside the repository workflow
