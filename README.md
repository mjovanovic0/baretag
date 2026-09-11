# Baretag

**Boot. Scan. Know your hardware.**

Baretag collects a server's hardware inventory and displays it as a console
report and a QR code. Boot from USB or virtual media, scan the screen with your
phone, and take the machine's serial numbers, network interfaces, and disk
details with you. The server can stay offline throughout the process.

Built for technicians and infrastructure teams identifying bare-metal machines
at the rack or through a remote console.

- **Offline collection:** read hardware facts directly from Linux, without a
  network service or a connection to an inventory server.
- **Bootable workflow:** add Baretag to a CoreOS live ISO with one command.
- **Portable results:** export JSON or decode a scanned QR payload on your
  workstation.
- **Adaptive display:** fit the report and QR codes to the available console,
  using compression and multiple symbols when needed.
- **Built-in ISO verification:** check the embedded payload, image structures,
  and changes against the original image after baking.

## Quick start

### 1. Build Baretag

You need **Go 1.26 or newer**, Git, and Make on a Linux or macOS workstation.
The initial build downloads Go dependencies. Hardware collection runs on Linux;
the bootable image workflow currently targets **x86-64 (`linux/amd64`)** servers.

```sh
git clone https://github.com/mjovanovic/baretag.git
cd baretag
make
```

This produces two executables:

| File | Purpose |
| --- | --- |
| `dist/baretag` | Run on your workstation to prepare images and decode scans. |
| `dist/baretag-linux-amd64` | Run on the server; this build is embedded in the ISO. |

The examples below run the workstation build from the repository. You can also
place it on your `PATH` and invoke it as `baretag`.

### 2. Prepare a bootable image

Start with an unmodified x86-64 CoreOS live ISO that provides the Ignition and
kernel-argument embed areas. Supply its path in place of `coreos-live.iso`:

```sh
./dist/baretag bake-into coreos-live.iso \
  --binary dist/baretag-linux-amd64 \
  -o baretag.iso
```

Baretag creates a copy, adds its payload and boot configuration, then verifies
the result. Use separate input and output paths. An existing output file is
rejected unless you pass `--force`.

Image preparation uses the Baretag executable itself; `xorriso`,
`coreos-installer`, `butane`, and a container runtime are not required.

### 3. Boot and scan

Attach `baretag.iso` as virtual media, or write the complete image to a USB
stick, and boot the server. The inventory and QR code appear above the login
prompt. Prefer a UEFI graphical console with enough resolution to show the
whole symbol; 1024×768 is a useful starting point.

Scan the QR code with your phone. For a remote management console, scan the
displayed console image. If the report contains several QR symbols, collect
every part.

Save the scanned text in `scan.txt`, with one QR payload per line, then decode
it on your workstation:

```sh
./dist/baretag decode < scan.txt > inventory.json
```

All parts must come from the same report. They can be supplied in any order.

## Use Baretag on a running Linux machine

Run the Linux build directly to display the current machine's inventory:

```sh
sudo ./dist/baretag-linux-amd64
```

Root access is needed to read protected DMI serial-number attributes. Collection
is best effort: unreadable attributes are left empty, and other devices are
still reported.

Export the collected facts as readable JSON:

```sh
sudo ./dist/baretag-linux-amd64 --json > inventory.json
```

To try the collectors from your workstation using the included hardware fixture:

```sh
./dist/baretag --root internal/inventory/testdata/r750 --json
```

The fixture supplies hardware attributes; the hostname and collection time
still come from the running process, and IP addresses are read from the host's
interface list.

## Collected data

| Category | Fields |
| --- | --- |
| Machine | Hostname, serial number and its source, vendor, product, UUID, collection timestamp. |
| Network interfaces | Name, MAC address, link speed, link state, and assigned IP addresses. |
| Disks | Device path, capacity in bytes, serial number, and model. |

Machine identity comes from `/sys/class/dmi/id`. Baretag chooses the first
meaningful chassis, product, or board serial, in that order, and ignores firmware
placeholders such as `To Be Filled By O.E.M.`.

Interface attributes come from `/sys/class/net`, with addresses supplied by the
kernel interface list. Loopback and link-local addresses are excluded. Physical
interfaces are included even when their links are down; most virtual interfaces
are filtered out by default.

Disks are whole block devices exposed by Linux, including logical disks exposed
by RAID controllers. Serial lookups fall back through sysfs attributes, SCSI VPD,
`/dev/disk/by-id`, world-wide identifiers, and finally `udevadm`. A reported serial
may therefore be a stable device identifier when a serial number is unavailable.
Partitions, optical drives, loop devices, and other non-disk or zero-size entries
are excluded by default.

Use `--all-nics` or `--all-disks` to broaden collection, and `--no-udev` to disable
the external `udevadm` fallback. Loopback interfaces remain excluded.

## QR payloads

Baretag chooses an encoding that fits the console:

| Format | Contents |
| --- | --- |
| Plain | Compact JSON with abbreviated keys and device arrays. |
| Packed | `QRINV1:` followed by gzip-compressed JSON encoded as base32. |
| Multipart | Pieces of a payload, each prefixed with `QRINVC1:<part>/<total>:`. |

These prefixes identify the current wire format. `decode` expands the compact
representation into JSON with descriptive field names. Packed payloads need
decoding before their contents are readable.

You can also pass a scan directly as a quoted argument. This complete plain
payload demonstrates the format without requiring a server:

```sh
./dist/baretag decode '{"h":"worker-01","s":"EXAMPLE-001","n":[],"d":[]}'
```

Compression reduces symbol size, but does not encrypt the inventory. Anyone
who can read the QR code can recover the included hardware details.

## Console layout and troubleshooting

The available screen area determines how much can be displayed. Baretag draws
QR modules with half-block characters so two vertical modules fit in one
terminal row. The default output uses black modules on a white background.

Automatic layout tries a full table, a compact device list, and finally an
identity line. The `--prefer` option controls the trade-off:

| Preference | Behavior |
| --- | --- |
| `payload` (default) | Try every report layout with full QR data before reducing payload detail. |
| `table` | Try reducing payload detail within each layout before giving up that layout. |

Minimal payloads omit the machine vendor, product, UUID, serial-source field,
and disk models. They retain the hostname, machine serial, collection time,
NIC facts, and disk paths, sizes, and serials. Use `--minimal` to select this
format explicitly. The live boot also saves the full inventory locally.

**If the QR code is clipped:** increase the actual console resolution. An 80×25
BIOS text console is often too small for a useful hardware inventory. Depending
on firmware and graphics support, you can request a larger mode when baking:

```sh
./dist/baretag bake-into coreos-live.iso \
  --binary dist/baretag-linux-amd64 \
  --karg video=1024x768 \
  -o baretag.iso
```

`--cols` and `--rows` override layout measurements; they do not change the display
resolution. If no arrangement fits, Baretag renders the result with an overflow
warning. A clipped symbol may be unreadable.

**If the scanner cannot read a complete symbol:** retain its light margin and
use the default color output. `--no-color` assumes a dark terminal background.

**If the report does not appear after boot:** check the boot service and saved
files below. The service waits for device enumeration and uses a bounded network
wait so a missing DHCP server does not hold up the report indefinitely.

### Live boot files

The booted system writes the report to these locations:

| Location | Contents |
| --- | --- |
| `/etc/issue.d/99-baretag.issue` | Login banner containing the report and QR symbols. |
| `/run/baretag/console.txt` | Report as laid out for the boot console. |
| `/run/baretag/report.txt` | Full text report. |
| `/run/baretag/inventory.json` | Full inventory as JSON. |
| `journalctl -u baretag.service` | Launcher diagnostics and the full report. |

These are live-session files. Export any results you need before shutting down.
To render the report again in a shell on the booted machine, run
`sudo /usr/local/bin/baretag`.

## How ISO preparation works

CoreOS live images reserve a small area for an Ignition configuration. The Go
executable is too large to embed there, so Baretag writes two pieces:

1. A small launcher and systemd unit go into the reserved Ignition area.
2. The compressed executable is appended beyond the original image, with offset,
   length, and SHA-256 metadata.

At boot, the launcher locates the source medium using the live image's volume
label, reads the appended payload by offset, verifies its checksum, and extracts
the executable. The service then renders the inventory on `/dev/tty1` and writes
the login banner before the console login starts.

Baking preserves the existing filesystem layout, partition tables, and boot
catalogue. Within the original image, it changes the Ignition embed area and,
when `--karg` is supplied, the reserved kernel-command-line regions. An existing
embedded Ignition configuration is replaced rather than merged.

The payload is not a file visible in the mounted ISO, so the complete baked
image must reach the boot medium. Filesystem cloning is used when supported;
otherwise, preparation copies the input image.

After baking, Baretag verifies the payload, executable architecture, volume
label, kernel arguments, Joliet tree, partition consistency, and embedded
configuration. It also compares the output with the input to detect changes
outside the permitted regions. These structural checks do not replace a boot
test on the target firmware.

To check a baked image again:

```sh
./dist/baretag verify-iso baretag.iso
```

This checks the image itself; comparison against the source happens during
`bake-into`.

## Command reference

```text
baretag [flags]                    Display this machine's inventory and QR code
baretag decode [payload ...]       Decode scans from arguments or standard input
baretag bake-into <iso> [flags]     Create and verify a customized CoreOS live ISO
baretag verify-iso <iso>           Verify an existing baked image
```

### Image preparation

| Flag | Default | Description |
| --- | --- | --- |
| `-o`, `--output` | `inventory.iso` | Output image path. |
| `--binary` | Current executable | Linux/amd64 executable to embed; required when the running build is for another platform. |
| `--karg` | None | Append a kernel argument; repeat for multiple arguments. |
| `--force` | Off | Allow overwriting an existing output file. |

### Collection and output

| Flag | Description |
| --- | --- |
| `--json` | Print the full inventory as JSON and exit. |
| `--payload` | Print the encoded payload without rendering QR symbols. |
| `--out <path>` | Also save the rendered report to a file. |
| `--issue <path>` | Write the rendered report as a login banner. |
| `--all-nics` | Include virtual interfaces normally filtered out. |
| `--all-disks` | Include block devices normally filtered out. |
| `--no-udev` | Disable the `udevadm` fallback for disk identifiers. |
| `--root <path>` | Read filesystem attributes from a captured tree. |

### Rendering

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `auto` | QR encoding: `auto`, `plain`, or `packed`. |
| `--layout` | `auto` | Report layout: `auto`, `full`, `compact`, or `qr` (identity line). |
| `--prefer` | `payload` | Preserve payload detail or the readable `table` first. |
| `--minimal` | Off | Omit descriptive fields from the QR payload. |
| `--tty` | None | Read console dimensions from a device such as `/dev/tty1`. |
| `--cols`, `--rows` | Detected | Override the available columns and rows. |
| `--reserve` | `0` | Leave rows free for a login prompt or other content. |
| `--clear` | Off | Clear the console before drawing. |
| `--no-footer` | Off | Omit the payload summary; overflow warnings remain. |
| `--level` | `L` | QR error correction: `L`, `M`, `Q`, or `H`. |
| `--quiet-zone` | `2` | Light margin around each symbol, in modules. |
| `--max-parts` | `4` | Maximum number of QR symbols for one payload. |
| `--no-color` | Off | Render without color escapes; assumes a dark background. |

## Development

```sh
make test
make lint
```

Tests cover hardware collection, payload encoding and decoding, console layout,
QR rendering, and ISO structures. QR tests read generated symbols with an
independent decoder. The hardware fixture covers a Dell PowerEdge R750 with
four NICs and five block devices, including NVMe, SCSI, SATA, and virtio cases.

Tests requiring a real CoreOS image are skipped by default. To include them:

```sh
make test-iso ISO=/path/to/coreos-live.iso
```

This sets `BARETAG_TEST_ISO` for the test run and includes a bake-and-verify
test. Allow space for a temporary copy of the image. It does not boot a VM or
physical server.

## Contributing

Bug reports, hardware compatibility reports, documentation improvements, and
pull requests are welcome. For a bug report, include the command, expected and
actual behavior, host OS and architecture, and relevant logs. For boot or QR
issues, also include the CoreOS image version, server model, boot mode, and
console dimensions.

Redact serial numbers, UUIDs, MAC addresses, and IP addresses before attaching
reports or QR images to a public issue. For code changes, run `make test` and
`make lint`; include the real-image tests when changing ISO handling and report
any physical boot testing you performed.

## License

Baretag is available under the [MIT License](LICENSE).
