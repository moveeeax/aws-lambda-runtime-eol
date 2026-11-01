# aws-lambda-runtime-eol

[![ci](https://github.com/moveeeax/aws-lambda-runtime-eol/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/aws-lambda-runtime-eol/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Find the AWS Lambda functions running a runtime that is already past its
end-of-life date — or about to be — before AWS blocks your next deploy.

AWS retires Lambda runtimes on a published schedule. After deprecation you stop
getting security patches; a bit later you can no longer update the function's
code or config, and eventually you can't create new functions on that runtime.
It's a slow-moving deadline that's easy to miss until a deploy suddenly fails.
`aws-lambda-runtime-eol` scans an account/region, reads each function's runtime,
and tells you exactly which ones are on borrowed time.

## How it works

- Enumerates every function in a region (paginated `ListFunctions`) via the AWS
  SDK and the standard credential chain.
- Compares each runtime against a **compiled-in table** of AWS deprecation
  dates, so classification is offline and deterministic. Override it any time
  with `--eol-data table.json`. The table follows the supported and deprecated
  runtime tables in the
  [Lambda developer guide](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html);
  a runtime that is not in it (for example one AWS has not scheduled for
  deprecation yet) is reported as `OK`.
- Classifies each function as `EXPIRED` (past EOL, **high**), `WARNING` (within
  `--within` days, default 90, **medium**), or `OK` (**none**).
- Prints a table or JSON, and can gate CI with `--fail-on high|medium`
  (non-zero exit when anything meets the threshold).

The classifier is a pure function with no AWS dependency, so you can run it
fully offline against a JSON file of functions — which is exactly how the tests
and the `--input` flag work.

## Install

```shell
go install github.com/moveeeax/aws-lambda-runtime-eol@latest
```

Or build from source:

```shell
git clone https://github.com/moveeeax/aws-lambda-runtime-eol.git
cd aws-lambda-runtime-eol
go build -o aws-lambda-runtime-eol .
```

## Usage

```shell
# Audit a region (uses your default AWS credentials)
aws-lambda-runtime-eol scan --region eu-west-1

# Warn on anything within 60 days of EOL, JSON for a dashboard
aws-lambda-runtime-eol scan --within 60 --output json > lambda-eol.json

# Gate CI: fail if any function is already expired
aws-lambda-runtime-eol scan --fail-on high
```

Run it offline against the bundled fixture (no AWS account needed):

```console
$ aws-lambda-runtime-eol scan --input examples/functions.json --now 2025-07-03 --within 90
FUNCTION         RUNTIME     STATUS   SEVERITY  EOL DATE    DAYS LEFT
billing-cron     python3.8   EXPIRED  high      2024-10-14  -262
thumbnailer      go1.x       EXPIRED  high      2024-01-08  -542
edge-router      nodejs18.x  WARNING  medium    2025-09-01  60
auth-authorizer  python3.12  OK       none      2028-10-31  1216
image-resize     nodejs20.x  OK       none      2026-04-30  301

5 function(s) in input file examples/functions.json: 2 expired, 1 within 90d, 2 ok
```

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--region` | SDK default | AWS region to scan |
| `--within` | `90` | Warn on runtimes within this many days of EOL |
| `--output` | `table` | `table` or `json` |
| `--fail-on` | *(off)* | Exit non-zero if any finding is `high` or `medium` |
| `--input` | *(off)* | Read functions from a JSON file instead of AWS |
| `--eol-data` | *(built-in)* | Override the EOL table with a JSON file |
| `--now` | today (UTC) | Reference date for classification (`YYYY-MM-DD`) |

### Input / override formats

`--input` accepts a bare array or a `{"functions": [...]}` object:

```json
[
  { "name": "billing-cron", "runtime": "python3.8" },
  { "name": "edge-router",  "runtime": "nodejs18.x" }
]
```

`--eol-data` is a `{runtime: "YYYY-MM-DD"}` map that replaces the built-in table:

```json
{ "nodejs18.x": "2025-09-01", "python3.12": "2028-10-31" }
```

## CI

```yaml
- name: Check Lambda runtimes
  run: |
    aws-lambda-runtime-eol scan --region eu-west-1 --within 60 --fail-on high
```

## Notes

- The embedded EOL table is a snapshot (see `TableAsOf` in `internal/eol`, last
  reviewed 2026-11-02). AWS updates dates and adds runtimes; keep the tool
  current with `--eol-data` when a date changes.
- The tool is read-only: it calls `ListFunctions` and nothing else.

## License

MIT — see [LICENSE](LICENSE).
