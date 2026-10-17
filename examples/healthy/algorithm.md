# Algorithm

Source: [algorithm.dot](algorithm.dot).

The input and output boundaries are accounted for by promises in a separate
DOT file. This makes an omitted producer or consumer a link error rather than a
diagram-reading exercise.

```specdot
schema: specdot.contract/v1
for: graph.healthy.algorithm
kind: algorithm
summary: Receive one command, execute it, and return one result.
```
