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
| `audience-poi.json` | `audiences create` | The minimum: one POI dataset. Every audience file here also works with `audiences estimate create`, which sizes the audience without creating it. |
| `audience-two-datasets.json` | `audiences create` | Two datasets, so `operator` is required. |
| `audience-expression.json` | `audiences create` | Three datasets, (Apps OR WebDomain) AND POI, so an `expression` joins them instead of an `operator`. A Multi condition audience, enabled per organization (403 otherwise). It takes no analyses, `datastreams` or `refine`. |
| `audience-refine-crosspurchase.json` | `audiences create` | `refine` sits **inside** the dataset; `crosspurchase` sits at the **top level**. Both are permission-gated. |
| `audience-connectivity-switching.json` | `audiences create` | One `Connectivity` dataset, whose `connectivity_network_kind` is required, with the **top-level** `networkswitching` block, which is permission-gated and cannot be sent with `crosspurchase`. Flags build the same body: `--type connectivity --network-kind home --network Starlink --technology Satellite --min-days 3 --country USA --switch-from 2026-09 --switch-to 2026-10`, with the name, dates and `--provider`. `audiences estimate create` checks the block too, so drop it to size the audience without the permission. |
| `audience-skus.json` | `audiences create` | One `SKUs` dataset (available on request): merchant and product brand **keys**, the `value` column of the SKUs catalogs, never the names; at least one of `merchants`, `brands` and `categories`. `categories` takes category ids (`reference skus categories`), and `device_types`, `device_oses`, `device_makes` and `device_models` take the purchase-device values as the catalog holds them, in upper case (`reference skus device-values --field type`, `os`, `make` or `model`). No `signal_providers`, and `location` reads `countries` and `states` only. `max_devices_per_ip` is the Match Strictness, 1 to 5, default 3. Flags build the same body: `--type skus --merchant "Example Outdoor Store" --brand dove --brand "Dove Men+Care" --country USA --state TX --state CA --match-strictness 3`, with the name and dates. A SKUs estimate is always answered by a worker. |
| `activation.json` | `activations create --file` | From flags, an export needs `--audience-id`, `--endpoint-connection-id`, `--pricing-model-id` and at least one `--datastream`; without a datastream it completes and delivers nothing. Use this file for per-stream inputs or compression. Add `--wait` to follow it to Completed; `--timeout` defaults to 60m. |
| `cohort.json` | `cohorts create` | Swap `file_uri` for `upload_reference` to use `intuizi uploads put`. Or build it from flags - see the README. |
| `cohort-from-audience.json` | `cohorts create` | The audience must be Completed, and makes at most one live cohort. Swap `device_limit` for `freq_limit` + `freq_min`/`freq_max`, or `distance_limit` + `distance` (meters). It carries no `name`: the cohort takes the audience's, and the API ignores one sent here. |
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
intuizi reference connectivity networks --kind home  # Connectivity "connectivity_networks"
intuizi reference skus merchants --search outdoor  # SKUs "merchants": the value column
intuizi reference skus brands --search dove        # SKUs "brands": the value column
intuizi reference skus categories --search "dog food"  # SKUs "categories": the value column, an id
intuizi reference skus device-values --field os   # SKUs "device_oses" (also --field type, make, model)
intuizi reference common endpoint-connections     # endpoint_connection_id
intuizi reference common pricing-models --partner-id <id>
intuizi reference common datastreams --partner-id <id>  # an activation's datastreams[].id
intuizi reference common datastream-visualizations --dataset-type POI  # an audience's datastreams[].id
intuizi reference common schedule-frequencies     # recurrence.frequency
intuizi reference common schedule-windows         # recurrence.window_type
intuizi reference common schedule-endings         # recurrence.ending.type
```

## Gotchas these files encode

- Dates go in as `Y-m-d`. Reads echo them back as `MM/DD/YYYY` - different
  field, different format.
- `refine` works only on a single-dataset audience, POI or Apps.
- A `SKUs` dataset cannot sit next to a `Cohorts`, `Demographics`, `Origin`,
  `Connectivity` or `Deidentified` dataset, and its audience takes no
  `analyses`, `crossvisitation`, `crosspurchase` or `refine`.
- A `crosspurchase` window covers at most two calendar months and cannot run
  past the last settled purchase week.
- `crossvisitation` can be sent on create but never round-trips on the read.
  Neither does `networkswitching`: its results are read in
  `analysis_results.network_switching` of `audiences show <id> --json`.
- A `Connectivity` dataset's dates widen to whole Monday-to-Sunday weeks, and
  in a `networkswitching` block on it a month stands for its last full week,
  which must be built for the dataset's signal providers. The dates in
  `audience-connectivity-switching.json` go stale like the others.
- A `WebDomain` dataset's `start_date` must fall within the last 45 days
  (today minus 46 days, in UTC, is the earliest accepted), or the API
  rejects it with a 422. `audience-two-datasets.json` carries a fixed date
  that goes stale, so move both datasets' windows to a recent week before
  sending.
