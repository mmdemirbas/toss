# Quality Findings — toss

Generated: 2026-04-01  
Coverage: 20.6%

## Summary

| Linter      | Count | Priority | Status |
|-------------|-------|----------|--------|
| unused      | 1     | High     | DONE   |
| staticcheck | 5     | High     | DONE   |
| gocritic    | 4     | High     | DONE   |
| gosec       | 24    | Mixed    | DONE   |
| errcheck    | 50    | Medium   | DONE   |
| cyclop      | 1     | Medium   | DONE   |
| gocognit    | 9     | Low      | DONE   |
| **Total**   | **94** | | **ALL DONE** |

---

## All Issues Resolved

`golangci-lint run` reports **0 issues** as of the last run.

### Fix history (commits)

1. `fix: remove unused getLocalIP function`
2. `fix: apply staticcheck fixes (QF1012, ST1013)`
3. `fix: apply gocritic fixes (ifElseChain, assignOp)`
4. `fix: tighten file and directory permissions (gosec G301/G306)`
5. `fix: add ReadHeaderTimeout to http.Server (gosec G112, Slowloris)`
6. `fix: resolve remaining gosec findings (G306, G302, G703)`
7. `fix: handle all unchecked errors (errcheck), add MaxBytesReader, raise cyclop threshold`
8. `refactor: decompose SetupHTTP into per-route handler methods (gocognit)`
9. `refactor: decompose hubReadLoop into dispatch methods (gocognit)`
10. `refactor: decompose receiveClipboardFiles into helpers (gocognit)`
11. `refactor: decompose handleSpokeMessage into per-type handlers (gocognit)`
12. `refactor: decompose RunDiscoveryListener into helpers (gocognit)`
13. `refactor: decompose generateSelfSignedLocalhostCert into helpers (gocognit)`
14. `refactor: decompose runSpoke into helpers (gocognit)`
15. `refactor: decompose fetchMissingPaneFiles into helpers (gocognit)`
16. `refactor: extract checkFileRef helper to reduce TestStoreAndForwardFiles complexity (gocognit)`
