package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/mjovanovic/baretag/internal/bake"
)

// repeatable collects a flag that may be given more than once.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, " ") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

func bakeMain(args []string) error {
	fs := flag.NewFlagSet("bake-into", flag.ContinueOnError)
	output := fs.String("o", "inventory.iso", "where to write the customised ISO")
	fs.StringVar(output, "output", "inventory.iso", "where to write the customised ISO")
	binaryPath := fs.String("binary", "", "the linux/amd64 baretag build to bake in (default: this executable)")
	force := fs.Bool("force", false, "overwrite the output file if it exists")
	var kargs repeatable
	fs.Var(&kargs, "karg", "kernel argument to add to the live boot; repeatable")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: baretag bake-into <coreos-live.iso> [flags]

Writes a copy of the ISO with baretag baked in, so that booting it shows
the machine's inventory and a QR code on the console.

Nothing else has to be installed. The binary is appended to the ISO filesystem
and a small launcher and systemd unit go into the region the ISO reserves for
an Ignition config. The bytes the machine boots from are not rewritten.

flags:
`)
		fs.PrintDefaults()
	}
	rest, err := parsePositional(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one input ISO")
	}

	binary, source, err := resolveBinary(*binaryPath)
	if err != nil {
		return err
	}

	input := rest[0]
	fmt.Printf("input      %s\n", input)
	fmt.Printf("binary     %s (%d bytes)\n", source, len(binary))
	fmt.Printf("output     %s\n", *output)
	if len(kargs) > 0 {
		fmt.Printf("kernel     +%s\n", strings.Join(kargs, " +"))
	}
	fmt.Println("copying and patching...")

	res, err := bake.Run(bake.Options{
		Input:  input,
		Output: *output,
		Binary: binary,
		Kargs:  kargs,
		Force:  *force,
	})
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("wrote %s (%s)\n", res.Output, humanBytes(res.Size))
	fmt.Printf("  volume label   %s\n", res.Label)
	fmt.Printf("  payload        %s appended, %s unpacked\n",
		humanBytes(int64(res.PayloadBytes)), humanBytes(int64(res.BinaryBytes)))
	fmt.Printf("  Ignition       %d bytes of a %s embed area\n", res.ConfigBytes, humanBytes(res.AreaBytes))
	fmt.Printf("  kernel args    %s\n", res.KernelArgs)
	fmt.Printf("  image grew by  %s; nothing already in it was rewritten\n", humanBytes(res.Grew))
	fmt.Println()
	fmt.Println("verifying...")

	report, err := bake.VerifyAgainst(res.Output, input)
	if err != nil {
		return err
	}
	return printReport(report)
}

// resolveBinary finds the linux/amd64 build to bake in. Running on Linux the
// tool can simply bake itself, which is the whole point of the subcommand;
// from a workstation of another kind the cross compiled build has to be named.
func resolveBinary(path string) ([]byte, string, error) {
	if path == "" {
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			return nil, "", fmt.Errorf(
				"this is a %s/%s build, which cannot run on the server\n"+
					"      build the Linux one and point at it:\n"+
					"        make build\n"+
					"        baretag bake-into <iso> --binary dist/baretag",
				runtime.GOOS, runtime.GOARCH)
		}
		self, err := os.Executable()
		if err != nil {
			return nil, "", fmt.Errorf("locate this executable: %w", err)
		}
		path = self
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if err := bake.CheckELF(data); err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return data, path, nil
}

func verifyMain(args []string) error {
	fs := flag.NewFlagSet("verify-iso", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: baretag verify-iso <iso>\n\n"+
			"Checks that an ISO still boots the way it did and that the\n"+
			"customisations are really in it.\n")
	}
	rest, err := parsePositional(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one ISO")
	}

	report, err := bake.Verify(rest[0])
	if err != nil {
		return err
	}
	return printReport(report)
}

func printReport(report *bake.Report) error {
	for _, c := range report.Checks {
		status := "ok  "
		if !c.OK {
			status = "FAIL"
		}
		fmt.Printf("  %s  %s\n", status, c.Name)
		if c.Detail != "" {
			fmt.Printf("        %s\n", c.Detail)
		}
	}
	if !report.OK() {
		return fmt.Errorf("%d check(s) failed", report.Failures())
	}
	fmt.Println("\nall checks passed")
	return nil
}

// parsePositional parses flags that may appear before or after the positional
// arguments. The standard parser stops at the first thing that is not a flag,
// which would reject the obvious way to type this: bake-into some.iso -o out.iso
func parsePositional(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
