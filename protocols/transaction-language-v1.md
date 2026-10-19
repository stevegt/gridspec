# GridSpec Transaction Language Version 1

Status: frozen protocol specification.

The protocol CID is CIDv1 raw/SHA-256 over this file's exact bytes.

A program is UTF-8 text with LF line endings and begins with `gridspec 1`.
Statements are executed in order and the whole program is atomic. Whitespace and
comments beginning with `#` are not semantically significant.

The language has these statements:

```text
module STRING
profile STRING
put KIND ID { FIELD = VALUE ... }
delete KIND ID
assert exists KIND ID
assert absent KIND ID
assert equals KIND ID FIELD VALUE
```

`KIND`, `ID`, and `FIELD` use identifiers. Values are strings, integers,
booleans, null, arrays, and maps. A trailing semicolon is optional. Duplicate
fields and duplicate map keys are invalid.

`put` replaces the exact `(KIND, ID)` entity. `delete` requires that exact
entity to exist. Assertions fail the entire transaction when false. `module`
and `profile` replace state metadata and may occur at most once.

There are no loops, recursion, functions, imports, file or network access,
clocks, randomness, process execution, or implementation-defined operations.
Evaluation therefore terminates after a number of steps bounded by the program
length. Integer values are signed 64-bit values. Implementations reject invalid
UTF-8, CR line endings, a byte-order mark, unknown statements, and trailing
input.

