# Contributing

Patches welcome. The project is MIT-licensed and stays that way; contributing
does not assign your copyright.

## Sign your work

Every commit must carry a Developer Certificate of Origin sign-off — a line at
the end of the commit message:

```
Signed-off-by: Your Name <you@example.com>
```

`git commit -s` adds it for you. It certifies the statement at
[developercertificate.org](https://developercertificate.org): that you wrote
the change or otherwise have the right to submit it under the project's
license. Use your real name and a reachable address.

This is provenance, not paperwork for its own sake — it records that every line
in the tree arrived under terms its author could actually offer.

## Before opening a PR

- `go build ./...` and `go test ./...` pass.
- For the Flutter client: `flutter analyze` and `flutter test` in `mobile/`
  pass.
- Code reads like the code around it. Comments explain why, not what.
