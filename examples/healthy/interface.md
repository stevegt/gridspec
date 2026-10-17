# Promise interface

Source: [interface.dot](interface.dot).

The two agents make autonomous, complementary promises. An offer is not an
action or a guarantee; its compatible acceptance is represented separately.

```specdot
schema: specdot.contract/v1
for: graph.healthy.interface
kind: promise
summary: The autonomous client/worker promise interface.
```

## Command offer

The client promises to provide a nominal `type.command` value.

## Command acceptance

The worker promises to accept that command type.

## Result offer

The worker promises to provide a nominal `type.result` value.

## Result acceptance

The client promises to accept that result type.
