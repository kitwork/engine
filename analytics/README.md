# Analytics

`analytics` is the first pure-Go analytics core for Kitwork and KitDB.

## Current slice

- immutable column segments;
- typed schema validation;
- zone-map pruning for `Eq`, `Between`, and prefix-style filters;
- streaming scans that avoid materializing rows unless requested;
- grouped aggregates for `COUNT`, `SUM`, `MIN`, `MAX`, and `AVG`;
- in-memory compaction by rebuilding one fresh immutable segment.
- durable segment files and a reopenable JSON manifest through `OpenDiskStore`.

## Example

```go
schema, _ := analytics.NewSchema(
    analytics.Column{Name: "tenant", Kind: analytics.KindText},
    analytics.Column{Name: "price", Kind: analytics.KindFloat64},
)

store := analytics.NewStore(schema)
_ = store.Append([]analytics.Row{
    {"tenant": "acme", "price": 12.5},
    {"tenant": "acme", "price": 19.0},
})

disk, _ := analytics.OpenDiskStore("./tenant.analytics", schema)
_ = disk.Append([]analytics.Row{{"tenant": "acme", "price": 12.5}})

tenant, _ := analytics.Eq("tenant", "acme")
count, _ := store.Count(context.Background(), tenant)
groups, _ := store.GroupBy(
    context.Background(),
    []string{"tenant"},
    nil,
    analytics.Count("rows"),
    analytics.SumFloat64("price", ""),
)
```

## Next slices

- durable column segment files;
- page-aware compression and spill;
- ordered change feed from KitDB;
- vectorized execution kernels;
- SQL / `struct()` / AI-assisted plan generation on top of the core.
