# Example payloads

Bodies for the `create` commands that take `--file`. Every id and code in them
is a placeholder - replace them with values read from your own account before
sending anything. The dates are examples too, and some go stale: see the
`WebDomain` note under Gotchas.

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
| `activation.json` | `activations create --file` | From flags, an export needs `--audience-id`, `--endpoint-connection-id`, `--pricing-model-id` and at least one `--datastream`; without a datastream it completes and delivers nothing. Use this file for per-stream inputs or compression. Add `--wait` to follow it to Completed; `--timeout` defaults to 60m. |
| `cohort.json` | `cohorts create` | Swap `file_uri` for `upload_reference` to use `intuizi uploads put`. Or build it from flags - see the README. |
| `cohort-from-audience.json` | `cohorts create` | The audience must be Completed, and makes at most one live cohort. Swap `device_limit` for `freq_limit` + `freq_min`/`freq_max`, or `distance_limit` + `distance` (meters). Its `name` is ignored - the cohort takes the audience's. |
| `schedule.json` | `schedules create` | The `activation` block re-exports the audience every cycle; drop it for a refresh-only schedule, which flags can build. `recurrence.start` must be in the future in `recurrence.timezone`. |

## Where the ids come from

```bash
intuizi reference common dataset-types            # dataset "type" values
intuizi reference common signal-providers --data-type POI
intuizi reference common countries                # location.countries
intuizi reference poi categories                  # POI "categories"
intuizi reference apps categories                 # Apps "categories"
intuizi reference web iab-categories              # WebDomain "iab_category_codes": the id column, not the code
intuizi reference transactions categories         # crosspurchase targets
intuizi reference common endpoint-connections     # endpoint_connection_id
intuizi reference common pricing-models --partner-id <id>
intuizi reference common datastreams --partner-id <id>  # datastreams[].id
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
- A `WebDomain` dataset's `start_date` must fall within the last 45 days
  (today minus 46 days, in UTC, is the earliest accepted), or the API
  rejects it with a 422. `audience-two-datasets.json` carries a fixed date
  that goes stale, so move both datasets' windows to a recent week before
  sending.
