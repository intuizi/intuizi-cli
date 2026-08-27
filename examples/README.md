# Example payloads

Bodies for the `create` commands that take `--file`. Every id and code in them
is a placeholder - replace them with values read from your own account before
sending anything.

> **`signal_providers` fails silently.** A value that is not in your account's
> catalog is *not* rejected with a 422 - the audience builds normally and
> completes with `results_count: 0`. The `BID001` in these files comes from the
> API docs and is not a real provider on any environment, so read yours first:
>
> ```bash
> intuizi reference common signal-providers --data-type POI
> ```
>
> Verified on staging 2026-08-27: audience 1363 built from `audience-poi.json`
> reached Completed with 0 devices for exactly this reason.

| File | Command | Notes |
| --- | --- | --- |
| `audience-poi.json` | `audiences create` | The minimum: one POI dataset. |
| `audience-two-datasets.json` | `audiences create` | Two datasets, so `operator` is required. |
| `audience-refine-crosspurchase.json` | `audiences create` | `refine` sits **inside** the dataset; `crosspurchase` sits at the **top level**. Both are permission-gated. |
| `activation.json` | `activations create --file` | A minimal export is three ids and needs no file at all. |
| `cohort.json` | `cohorts create` | Swap `file_uri` for `upload_reference` to use `intuizi uploads put`. |
| `schedule.json` | `schedules create` | `recurrence.start` must be in the future. |

## Where the ids come from

```bash
intuizi reference common dataset-types            # dataset "type" values
intuizi reference common signal-providers --data-type POI
intuizi reference common countries                # location.countries
intuizi reference poi categories                  # POI "categories"
intuizi reference apps categories                 # Apps "categories"
intuizi reference affinity-transactions categories # crosspurchase targets
intuizi reference common endpoint-connections     # endpoint_connection_id
intuizi reference common pricing-models --partner-id <id>
intuizi reference common schedule-frequencies     # recurrence.frequency
intuizi reference common schedule-windows         # recurrence.window_type
intuizi reference common schedule-endings         # recurrence.ending.type
```

## Gotchas these files encode

- Dates go in as `Y-m-d`. Reads echo them back as `MM/DD/YYYY` - different
  field, different format.
- `refine` works only on a single-dataset audience, POI or Apps.
- A `crosspurchase` window covers at most two calendar months and cannot run
  past the last settled purchase week.
- `crossvisitation` can be sent on create but never round-trips on the read.
