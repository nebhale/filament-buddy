# Third-party notices

Filament Buddy is Apache-2.0. The Go runtime, YAML parser, SQLite implementation,
and their dependencies retain their licenses. Container images include exact Go
module license files in `/usr/share/licenses/filament-buddy/go` and the Go
license alongside them. Alpine packages retain their original licenses.

Each [GitHub release](https://github.com/nebhale/filament-buddy/releases) includes
architecture-specific source archives and SHA-256 checksums for the image's
Alpine components, including BusyBox. These archives contain the exact package
inventory, aports recipes at the recorded commits, patches and configuration,
and upstream archives checked against recipe SHA-512 hashes. Original copyright
and license notices are included in those sources. Missing or mismatched source
prevents image publication. Preserve these assets when redistributing an image;
sources from a newer package version do not replace its corresponding sources.

The Apache-2.0 image label describes Filament Buddy's own source, not every
component in the container. License texts are also installed under
`/usr/share/licenses/filament-buddy`.

The application does not bundle FFmpeg, cameras, Spoolman, or samplicator.
Samplicator is a separately deployed service with its own licensing.
