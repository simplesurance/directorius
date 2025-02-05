# Directorius

Directorius provides a merge queue for GitHub pull requests (PR).
It relies on GitHub webhook events, its REST and graphQL API, branch protection
rules and can work together with Jenkins.

A pull request enters the queue when auto-merge is enabled or a
configurable label is added to it.
Directorius maintains 2 states for PRs:

- an ordered active queue for PRs that are ready to be processed
- an unordered suspend queue for PRs that do not meet the merge requirements
  configured on GitHub.

For the first PR in the active queue, Directorius:

- schedules builds of Jenkins CI jobs
- syncs the PR branch with changes in it's base branch
- marks it with a queue head label
- reports a positive directorius commit status to GitHub.

When all GitHub merge requirements are fulfilled, the PR is expected to be
merged automatically (automerge) by GitHub.

It comes with a basic webinterface that allows viewing the queues and
prioritizing PRs.

## Setup 

### Configuration File

A documented example configuration file can be found in the repository:
[config.example.toml](config.example.toml).

### Typical GitHub Repository Setup

GitHub Repository Settings:

- Branch protection rules:
  - Require a pull request before merging
  - Require approvals
  - Require status checks to pass before merging
    - require: directorius commit status
    - require: all other CI jobs that must succeed
  - Require branches to be up to date before merging
  - **Disable**: Require merge queue
- General:
  - **Enable**: Allow auto-merge
    (Directorius does not merge PRs, it relies on GitHub to do it when all
    prerequisites are fulfilled)


---

Directorius has been forked from the autoupdater component of
[goordinator](https://github.com/simplesurance/goordinator/) version 0.14.0.
