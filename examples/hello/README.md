# hello

The application and the host from the engine's `readme.md` (sections "An application" and "Run it"),
as a module that runs: one Go file, one manifest, one folder. Go 1.26.

```bash
go run .
```

`PORT` chooses the port; the manifest falls back to 8080. With the host up, four requests show it:

- `curl http://localhost:8080/` answers the page at `/`: an `<h1>` reading "Hello from a folder", rendered on the server from `app/content.ui`.
- `curl "http://localhost:8080/api/hello?name=Kitwork"` answers `{"name":"Kitwork","ok":true}`.
- `curl "http://localhost:8080/api/notes?text=first"` adds a row to `app/.data/app.db` and answers every row; the second call answers two rows.
- `curl http://localhost:8080/router.work` is a 404: source files are never served.

`app/api/upstream/router.work` is the `safe` listing from "The language": `/api/upstream` answers whatever the feed returns as JSON, or a 502 carrying the failure's code when the request itself fails.
