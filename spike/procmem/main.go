// Spike P0.7: apply D8's memory instrument to a process we did not write.
//
// spike/memprobe measures itself: it calls task_info on its own task and runs vmmap against
// os.Getpid(). Neither works for AltTab, and P0.7 needs AltTab's number — the project's headline
// goal is "less memory than AltTab" and docs/DECISIONS.md D6 records that no baseline was ever
// taken. This reads the same three D8 quantities out of `vmmap --summary <pid>` for any pid:
//
//	CG raster data            the region type that holds CGImage backing store — the thumbnails
//	Physical footprint        what Activity Monitor shows
//	Physical footprint (peak) the true high-water mark, and the only honest number for bitmaps
//
// D8's order-sensitivity warning applies with a twist. In-process we could fault pages back in by
// reading them; here we cannot touch another process's pixels, so RESIDENT is a *lower bound* that
// depends on how recently AltTab drew. VIRTUAL and peak are the stable numbers. Sample over time
// and summon the switcher while it runs — that is what -n and -every are for.
//
// A note earned the hard way: AltTab quits and relaunches itself when permissions are granted, so a
// pid captured once goes stale and every later sample fails. With -name the pid is re-resolved every
// sample and a restart is reported rather than fatal. Transient vmmap failures never end the run —
// a measurement session is minutes of a human pressing keys, and losing it to one bad sample is worse
// than a gap in the table.
//
// Usage:
//
//	go run ./spike/procmem -name AltTab -n 12 -every 5s
//	go run ./spike/procmem -pid 1234
//
// No cgo: it shells out to vmmap. That keeps it usable against any pid, including a release build
// of GoTab later, without linking it into the thing being measured.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func mb(b int64) float64 { return float64(b) / 1024 / 1024 }

// sample is one reading of the D8 quantities. Zero values mean vmmap did not report that row:
// a process holding no CGImages has no "CG raster data" region at all, which is itself a result.
type sample struct {
	at            time.Time
	cgVirtual     int64
	cgResident    int64
	cgDirty       int64
	totalVirtual  int64
	totalResident int64
	totalDirty    int64
	footprint     int64
	peak          int64
}

// parseSize turns vmmap's "249.8M" / "2.7G" / "16K" into bytes. Same grammar memprobe parses;
// duplicated rather than shared because spikes are throwaway probes, not a library.
func parseSize(s string) int64 {
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(mult))
}

// read runs vmmap once and pulls out every D8 quantity. One invocation, not one per metric:
// vmmap walks the whole VM map and takes ~100ms on a busy process, and two runs would report
// two different moments.
func read(pid int) (sample, error) {
	out, err := exec.Command("vmmap", "--summary", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		// vmmap refuses hardened-runtime processes without root. Say so plainly: the fix is
		// sudo, not a different instrument.
		return sample{}, fmt.Errorf("vmmap %d: %w\n%s", pid, err, strings.TrimSpace(string(out)))
	}
	s := sample{at: time.Now()}
	for _, l := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "Physical footprint (peak):"):
			s.peak = parseSize(strings.TrimSpace(strings.TrimPrefix(t, "Physical footprint (peak):")))
		case strings.HasPrefix(t, "Physical footprint:"):
			s.footprint = parseSize(strings.TrimSpace(strings.TrimPrefix(t, "Physical footprint:")))
		case strings.HasPrefix(t, "CG raster data"):
			if f := strings.Fields(strings.TrimPrefix(t, "CG raster data")); len(f) >= 3 {
				s.cgVirtual, s.cgResident, s.cgDirty = parseSize(f[0]), parseSize(f[1]), parseSize(f[2])
			}
		case strings.HasPrefix(t, "TOTAL") && !strings.Contains(t, "TOTAL, but"):
			if f := strings.Fields(t); len(f) >= 4 {
				s.totalVirtual, s.totalResident, s.totalDirty = parseSize(f[1]), parseSize(f[2]), parseSize(f[3])
			}
		}
	}
	return s, nil
}

// pidOf finds a process by executable name. pgrep -x so "AltTab" does not also match a helper
// or this probe's own command line.
func pidOf(name string) (int, error) {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0, fmt.Errorf("no running process named %q", name)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("no running process named %q", name)
	}
	if len(fields) > 1 {
		return 0, fmt.Errorf("%d processes named %q (%s) — pass -pid to pick one",
			len(fields), name, strings.Join(fields, " "))
	}
	return strconv.Atoi(fields[0])
}

func main() {
	name := flag.String("name", "", "process name to measure, e.g. AltTab")
	pid := flag.Int("pid", 0, "process id to measure (overrides -name)")
	n := flag.Int("n", 1, "number of samples")
	every := flag.Duration("every", 5*time.Second, "interval between samples")
	flag.Parse()

	target := *pid
	if target == 0 {
		if *name == "" {
			fmt.Fprintln(os.Stderr, "need -name or -pid")
			os.Exit(2)
		}
		var err error
		if target, err = pidOf(*name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fmt.Printf("measuring pid %d", target)
	if *name != "" {
		fmt.Printf(" (%s)", *name)
	}
	fmt.Printf(", %d sample(s) every %s\n\n", *n, *every)
	fmt.Printf("%8s  %28s  %26s  %10s\n", "", "CG raster data", "TOTAL", "footprint")
	fmt.Printf("%8s  %8s %8s %8s  %8s %8s %8s  %10s\n",
		"elapsed", "virtual", "resident", "dirty", "virtual", "resident", "dirty", "peak")

	var first, last, max sample
	var got, missed int
	start := time.Now()
	for i := 0; i < *n; i++ {
		if i > 0 {
			time.Sleep(*every)
		}
		// Re-resolve by name every sample: the target may have restarted under a new pid.
		if *name != "" {
			if p, err := pidOf(*name); err == nil && p != target {
				fmt.Printf("%7.0fs  -- %s restarted: pid %d -> %d (peak resets with it)\n",
					time.Since(start).Seconds(), *name, target, p)
				target = p
			} else if err != nil {
				missed++
				fmt.Printf("%7.0fs  -- %s not running\n", time.Since(start).Seconds(), *name)
				continue
			}
		}
		s, err := read(target)
		if err != nil {
			// A dead or momentarily unreadable target is a gap, not the end of the session.
			missed++
			fmt.Printf("%7.0fs  -- unreadable\n", time.Since(start).Seconds())
			continue
		}
		if got == 0 {
			first = s
		}
		got++
		last = s
		if s.cgResident > max.cgResident {
			max.cgResident, max.cgVirtual = s.cgResident, s.cgVirtual
		}
		if s.peak > max.peak {
			max.peak = s.peak
		}
		fmt.Printf("%7.0fs  %7.1fM %7.1fM %7.1fM  %7.1fM %7.1fM %7.1fM  %9.1fM\n",
			s.at.Sub(start).Seconds(),
			mb(s.cgVirtual), mb(s.cgResident), mb(s.cgDirty),
			mb(s.totalVirtual), mb(s.totalResident), mb(s.totalDirty),
			mb(s.peak))
	}

	fmt.Println()
	if got == 0 {
		fmt.Printf("NO SAMPLES: %d attempts, none readable. Nothing was measured.\n", missed)
		os.Exit(1)
	}
	if missed > 0 {
		fmt.Printf("%d of %d samples were unreadable\n", missed, got+missed)
	}
	// The headline numbers. Resident is a floor that depends on how recently the target drew;
	// peak is the kernel's high-water mark and does not decay, so it is the honest one to quote.
	fmt.Printf("MAX observed: CG raster resident %.1f MB (of %.1f MB virtual), footprint peak %.1f MB\n",
		mb(max.cgResident), mb(max.cgVirtual), mb(max.peak))

	fmt.Printf("footprint now %.1f MB, peak %.1f MB\n", mb(last.footprint), mb(last.peak))
	if last.cgVirtual == 0 {
		// Not a failure. It means this process is holding no CGImage backing store right now,
		// which for a switcher that caches thumbnails is a finding worth writing down.
		fmt.Println("no \"CG raster data\" region: the process holds no CGImage backing store at this moment")
	} else {
		fmt.Printf("CG raster data %.1f MB virtual, %.1f MB resident (%.0f%% of virtual is faulted in)\n",
			mb(last.cgVirtual), mb(last.cgResident),
			100*float64(last.cgResident)/float64(last.cgVirtual))
	}
	if got > 1 {
		fmt.Printf("growth over %.0fs: CG raster virtual %+.1f MB, footprint %+.1f MB\n",
			last.at.Sub(first.at).Seconds(),
			mb(last.cgVirtual-first.cgVirtual), mb(last.footprint-first.footprint))
	}
}
