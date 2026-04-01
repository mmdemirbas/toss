# Quality Findings — toss

Generated: 2026-04-01  
Coverage: 20.6%

## Summary

| Linter      | Count | Priority |
|-------------|-------|----------|
| errcheck    | 50    | Medium — handle returned errors |
| gosec       | 24    | Mixed — see notes below |
| gocognit    | 9     | Low — refactors required |
| staticcheck | 5     | High — easy mechanical fixes |
| gocritic    | 4     | High — easy mechanical fixes |
| cyclop      | 1     | Medium — small refactor |
| unused      | 1     | High — dead code |
| **Total**   | **94** | |

---

## Fix Order

### DONE

### TODO

#### 1. unused — remove dead code
- [ ] `discovery.go:205` `getLocalIP` is unused → delete

#### 2. staticcheck — mechanical fixes
- [ ] `clipboard_files.go:110` `QF1012` WriteString(fmt.Sprintf) → fmt.Fprintf
- [ ] `node.go:931` `QF1012` WriteString(fmt.Sprintf) → fmt.Fprintf
- [ ] `handlers.go:86` `ST1013` numeric 405 → http.StatusMethodNotAllowed
- [ ] `handlers.go:135` `ST1013` numeric 405 → http.StatusMethodNotAllowed
- [ ] `handlers.go:142` `ST1013` numeric 405 → http.StatusMethodNotAllowed

#### 3. gocritic — mechanical fixes
- [ ] `node.go:394` if-else chain → switch
- [ ] `node.go:728` if-else chain → switch
- [ ] `node.go:1044` if-else chain → switch
- [ ] `node.go:550` `backoff = backoff * 2` → `backoff *= 2`

#### 4. gosec G301/G306 — file/dir permissions
- [ ] `api_test.go:22` `G301` os.MkdirAll perm 0755 → 0750
- [ ] `store.go:34` `G301` MkdirAll perm → 0750
- [ ] `tls.go:18` `G301` MkdirAll perm → 0750
- [ ] `clipboard.go:227` `G306` WriteFile perm → 0600
- [ ] `node.go:842` `G306` WriteFile perm → 0600
- [ ] `node.go:899` `G306` WriteFile perm → 0600

#### 5. gosec G112 — Slowloris
- [ ] `main.go:58` Add ReadHeaderTimeout to http.Server

#### 6. gosec — suppress intentional by-design
- [ ] `G402` TLS InsecureSkipVerify (node.go:22) — LAN design, add to .golangci.yml excludes
- [ ] `G204` subprocess with variable (clipboard_image.go, clipboard_files.go) — unavoidable for platform clipboard ops
- [ ] `G501/G401` MD5 usage — echo-prevention hash only, not security-sensitive
- [ ] `G304` file inclusion via variable — temp files created by the program itself
- [ ] `G706` log injection (handlers.go) — review; accept or sanitize
- [ ] `G704` SSRF (handlers.go) — review; internal relay to spoke nodes, expected

#### 7. errcheck — handle returned errors (50 issues)
Production code (non-test):
- [ ] `clipboard.go:231` os.Remove
- [ ] `clipboard_image.go:91` os.Remove
- [ ] `clipboard_image.go:172` f.Close
- [ ] `clipboard_image.go:183` f.Close
- [ ] `discovery.go:31` conn.Close
- [ ] `discovery.go:38` conn.SetReadDeadline
- [ ] `discovery.go:59` conn.WriteTo
- [ ] `discovery.go:70` conn.WriteTo
- [ ] `discovery.go:93` conn.Close
- [ ] `discovery.go:104` conn.SetReadDeadline
- [ ] `discovery.go:123` conn.WriteTo
- [ ] `discovery.go:152` conn.Close
- [ ] `handlers.go:44` json.Encoder.Encode
- [ ] `handlers.go:52` json.Encoder.Encode
- [ ] `handlers.go:81` node.SendToHub
- [ ] `handlers.go:84` json.Encoder.Encode
- [ ] `handlers.go:120` node.SendToHub
- [ ] `handlers.go:130` node.SendToHub
- [ ] `handlers.go:145` r.ParseMultipartForm
- [ ] `handlers.go:151` file.Close
- [ ] `handlers.go:170` dst.Close
- [ ] `handlers.go:235` dst.Close
- [ ] `handlers.go:236` os.Remove
- [ ] `handlers.go:239` dst.Close
- [ ] `handlers.go:304` f.Close
- [ ] `handlers.go:314` writer.Close
- [ ] `main.go:112` fmt.Fprintf
- [ ] `main.go:127` fmt.Fprintf
- [ ] `node.go:185` c.conn.Close
- [ ] `node.go:259` conn.SetReadDeadline
- [ ] `node.go:273` conn.Close (+ others in node.go)
- [ ] `node.go:419` client.conn.Close
- [ ] `node.go:460` client.conn.Close
- [ ] `node.go` json.Unmarshal

Test code:
- [ ] `api_test.go:69` resp.Body.Close
- [ ] `api_test.go:100` resp.Body.Close
- [ ] `api_test.go:107` json.Decoder.Decode
- [ ] `api_test.go:136` resp.Body.Close
- [ ] `api_test.go:139` json.Decoder.Decode
- [ ] `api_test.go:152` json.Decoder.Decode
- [ ] `api_test.go:258` part.Write
- [ ] `api_test.go:259` w.Close
- [ ] `api_test.go:344` part.Write
- [ ] `api_test.go:345` w.Close
- [ ] `api_test.go:501` os.WriteFile
- [ ] `api_test.go:502` os.WriteFile
- [ ] `api_test.go:601` os.WriteFile
- [ ] `api_test.go:830` os.MkdirAll

#### 8. cyclop — refactor
- [ ] `clipboard.go:107` check() cyclomatic complexity 11 → split into sub-functions

#### 9. gocognit — major refactors
- [ ] `handlers.go:22` SetupHTTP complexity 108 → split into per-route handlers
- [ ] `node.go:326` hubReadLoop complexity 25
- [ ] `node.go:501` runSpoke complexity 20
- [ ] `node.go:660` handleSpokeMessage complexity 19
- [ ] `node.go:774` fetchMissingPaneFiles complexity 18
- [ ] `node.go:967` receiveClipboardFiles complexity 29
- [ ] `tls.go:32` generateSelfSignedLocalhostCert complexity 18
- [ ] `discovery.go:78` RunDiscoveryListener complexity 24
- [ ] `api_test.go:405` TestStoreAndForwardFiles complexity 17
