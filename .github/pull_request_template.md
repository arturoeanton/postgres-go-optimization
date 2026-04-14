<!--
Thanks for the contribution. Please read CONTRIBUTING.md before opening
the PR, and make sure the checklist below is green.
-->

## Summary

<!-- One or two sentences on what changes and why. -->

## Motivation

<!--
Why is this needed? Link to an issue if there is one. If this adds or
changes a rule, explain the PostgreSQL behaviour it is grounded in and
cite the source file or `GUIA_POSTGRES_ES_2.md` section.
-->

## Type of change

- [ ] New rule
- [ ] Bug fix in an existing rule
- [ ] Core / analyzer / runtime change
- [ ] JavaScript rule or loader change
- [ ] Documentation
- [ ] CI / build / tooling

## Checklist

- [ ] `go test -race -count=1 ./...` passes locally.
- [ ] `go vet ./...` is clean.
- [ ] If a rule is added or changed, `testdata/bad/<rule>.sql` (or
      `testdata/bad-js/<rule>.sql`) was added or updated.
- [ ] If a rule is added or changed, the README rule table was updated.
- [ ] A `CHANGELOG.md` entry under `[Unreleased]` was added.
- [ ] Commit subject follows the `rule: | core: | docs: | docker: | ci:`
      convention.
