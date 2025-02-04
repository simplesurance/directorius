# Directorius

Directorius implements a merge queue for GitHub pull requests (PR).
It relies on GitHub webhook events, its REST and graphQL API, branch protection
rules and can work together with Jenkins.

Pull requests are queued and processed in order.
When a PR is not approved, or has a required failed CI run it is moved to the
__suspend queue__.
When it's branch or it's base branch is updated, it is getting approved or the
status of a required failed CI check becomes positive it is moved to the __activ
e queue__.
The status of the first PR in the __active queue__ is monitored, Jenkins CI Jobs
are triggered for it, it is being kept up to date with its base branch, it is
labeled and a positive commit status is reported for it.
When all configured GitHub merge requirements are fulfilled, it is merged by
GitHub.

To enforce that only the PR that is first in the queue is merged, the
commit status submitted by directorius can be configured as merge requirement in
GitHub.

## Features

- Adds PRs to the queue when auto-merge is enabled or a PR label is added
- Automatically updates the first PR in the queue with changes in its base
  branch
- Supports different queues per repository and base branch
- Triggers Jenkins Jobs that report their status to GitHub and aren't running
- Ignores reports of CI Job results from older obsolete Jenkins builds
- Submits a GitHub Status and labels a PR when it is first in the merge queue
- Has a web interface to:
  - view queued PRs and their status
  - prioritize PRs,
  - pause the merge queue

## Configuration
A documented example configuration file can be found in the repository:
[config.example.toml](config.example.toml).

--
Directorius has been forked from the autoupdater component of
[goordinator](https://github.com/simplesurance/goordinator/) version 0.14.0.
