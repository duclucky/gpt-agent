## Summary

Describe the problem and the change.

## Scope

- What files/components changed?
- What intentionally did not change?

## Validation

List the checks that actually ran and their results. Do not mark unrun checks as passing.

- [ ] Relevant focused tests
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] PowerShell syntax validation when scripts changed
- [ ] Build/release check when packaging or installer behavior changed
- [ ] Runtime/manual verification when behavior requires it
- [ ] Final diff reviewed

## Security / privacy

- [ ] No API keys, tokens, credentials, private endpoints, private repository content, or machine-specific secrets are included.
- [ ] Workspace, shell, network, credential, and trust-boundary changes are documented when relevant.

## Compatibility / migration

Describe any breaking change, config/schema change, migration, or rollback concern. Write `None` if not applicable.

## UI changes

Include screenshots or a short description of rendered verification when the change affects visible UI. Write `None` if not applicable.
