# Automatic restaurant discovery

`POST /internal/discovery` requires an internal-operator bearer token. It captures
up to five restaurant pages and saves valid candidates as drafts. It never approves
stores, sets certification, or changes an existing listing.

```json
{
  "ward_id": 13,
  "query": "bakery",
  "limit": 5,
  "urls": []
}
```

With an empty `urls` list, the server searches Brave for the ward, optional query,
and gluten-free restaurant keywords. Set `BRAVE_SEARCH_API_KEY` on the server.
The provider contract is documented at
https://api-dashboard.search.brave.com/api-reference/web/search/get .
No search key is sent to the browser or restaurant pages. Provider redirects are
rejected so they cannot forward the credential.

Alternatively, supply one to five HTTP(S) restaurant page URLs. This bypasses
search and works without a provider key. Both modes use the same capture and
persistence flow. Unknown wards and malformed URLs fail before any writes.

## Capture and review

A page must expose exactly one Restaurant, CafeOrCoffeeShop, Bakery, or
FoodEstablishment in JSON-LD, with a name and address. Its visible text must mention
“gluten-free”, “gluten free”, or “グルテンフリー”. The captured address must match
the selected ward. These are discovery signals, not certification: negated claims
can still appear, so an operator must read the recorded evidence before approval.
Listicles, ambiguous branches, JavaScript-only pages and incomplete metadata return
per-source failures for manual capture.

The server saves name, address, phone, cuisine and source URL. Research notes keep
a timestamp, evidence excerpt, up to ten image URLs, up to ten menu links, and up
to 12,000 characters from the first linked HTML menu. PDF/image menus remain links;
OCR, arbitrary PDF uploads, image downloads, and automatic dish/price creation are
not part of this endpoint. No image is automatically assigned as a public photo.
Operators review reuse permission and use the existing photo/menu editors to
publish verified material.

The response has a `results` array, with one outcome per unique attempted URL:

```json
{
  "results": [
    {"url":"https://restaurant.example/", "name":"Rice Cafe", "status":"created", "store_id":"uuid", "warnings":[]},
    {"url":"https://another.example/", "status":"failed", "error":"capture failed"},
    {"url":"https://existing.example/", "status":"duplicate", "store_id":"uuid"}
  ]
}
```

Search failures return 502; an unconfigured search key returns 503 with instructions
to paste URLs instead. A successful batch with no matches returns an empty list.
Capture/save failures affect only their source. Duplicates link to the existing
store and do not overwrite it, regardless of its approval status.

## Execution and retries

This is a synchronous, bounded request, not a background worker: five sources,
a 90-second batch deadline, 12-second fetch deadlines, a 2 MiB page limit, and at
most three redirects. Each source follows at most one menu link. Pages are fetched
without browser credentials, cookies, JavaScript execution, or proxy inheritance.
Private/reserved IPs are blocked during DNS resolution and redirects; connections
dial the validated address to prevent rebinding. Reverse-proxy timeouts should
allow at least 95 seconds for the endpoint.

Each candidate commits independently. A database advisory transaction lock
serializes discovery duplicate checks across server instances. The check matches
source URL globally or name/address within a ward. Retrying a partially completed
batch returns duplicates for previously committed rows. The lock coordinates
this endpoint; existing manual creation and CSV import do not take that lock.
No schema migration is required beyond the existing store migrations.

## Validation

```bash
go test ./...
# Optional database concurrency test, only against a disposable migrated database:
DISCOVERY_TEST_DB='postgres://.../test_database?sslmode=disable' go test -race ./db/sqlc
```

Tests cover extraction, ambiguous pages, missing evidence, provider credentials,
menu fetching, fetch limits, SSRF protections, internal-role enforcement, request
validation, partial results and concurrent duplicate prevention.
