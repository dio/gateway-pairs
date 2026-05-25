# Named pair (suffix-based)

Instead of numeric index, use a string suffix: `prod`, `staging`, `team-a`, etc.

```
pair.index: 0          (tells the chart: suffix IS the identity, not the index)
pair.nameSuffix: prod  (the actual suffix)
```

Resulting names:
  System NS:    tr-system-prod
  Dataplane NS: tr-dataplane-prod
  GatewayClass: tr-prod
  controllerName: gateway.envoyproxy.io/tr-prod

Use `gwp pair info --suffix prod` to confirm.
