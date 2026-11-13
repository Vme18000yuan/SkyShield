# Pinned Norma runtime

Imported from `github.com/Autumn-27/norma v0.4.3`, matching the existing
`go.sum` dependency. Original package names and source notices are preserved.
Server and the ARTEX adapter use this checked-in copy, including in Docker builds.

The upstream author added the Apache-2.0 license in commit
`52f17dd52d95b087265e5d5b306fe85c1ea05599` immediately after v0.4.3; that
commit changes only the license file. Its original license text is included
in this directory.

SkyShield patches make transcript persistence failures observable, stop before
executing tools whose intent could not be recorded, preserve uncertain tool
outcomes on recovery, and enforce bounded run/settlement lifetimes. Tests live
beside the affected packages. The module cache and upstream checkout are untouched.
