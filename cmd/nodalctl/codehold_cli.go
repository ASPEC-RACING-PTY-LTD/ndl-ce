package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const codeholdCLIUsage = `usage: nodalctl codehold-cli show
       nodalctl codehold-cli settings [--url URL] [--auto-install true|false] [--concurrency N] [--timeout SECONDS]
       nodalctl codehold-cli rollout [--dry-run] [--update] [--include-stopped] [--concurrency N] [--timeout SECONDS] [--only ID,ID] [--json]
       nodalctl codehold-cli status [--json]`

// cmdCodeholdCLI manages the CodeHold CLI ("ch") in system containers.
func cmdCodeholdCLI(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", codeholdCLIUsage)
	}
	f := parseFlags(args[1:])
	switch args[0] {
	case "show":
		return cmdGet("/api/v1/codehold-cli")
	case "settings":
		raw, err := do("GET", "/api/v1/codehold-cli", nil, true)
		if err != nil {
			return err
		}
		var cur struct {
			Settings map[string]any `json:"settings"`
		}
		if err := json.Unmarshal(raw, &cur); err != nil {
			return err
		}
		st := cur.Settings
		if v, ok := f["url"]; ok {
			st["url"] = v
		}
		if v, ok := f["auto-install"]; ok {
			st["auto_install"] = v == "true"
		}
		for flag, key := range map[string]string{"concurrency": "concurrency", "timeout": "timeout_seconds"} {
			if v, ok := f[flag]; ok {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("--%s must be a number", flag)
				}
				st[key] = n
			}
		}
		return putJSON("/api/v1/codehold-cli/settings", st, true)
	case "rollout":
		body := map[string]any{"dry_run": f["dry-run"] == "true", "update": f["update"] == "true", "include_stopped": f["include-stopped"] == "true"}
		for flag, key := range map[string]string{"concurrency": "concurrency", "timeout": "timeout_seconds"} {
			if v, ok := f[flag]; ok {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("--%s must be a number", flag)
				}
				body[key] = n
			}
		}
		if v := f["only"]; v != "" {
			body["workload_ids"] = strings.Split(v, ",")
		}
		if _, err := do("POST", "/api/v1/codehold-cli/rollout", body, true); err != nil {
			return err
		}
		return waitCodeholdCLIRollout(f["json"] == "true")
	case "status":
		raw, err := do("GET", "/api/v1/codehold-cli/rollout", nil, true)
		if err != nil {
			return err
		}
		if f["json"] == "true" {
			fmt.Println(string(raw))
			return nil
		}
		r, err := parseCLIRollout(raw)
		if err != nil || r == nil {
			fmt.Println("No rollout has run since the control plane started.")
			return err
		}
		return printCLIRollout(r)
	}
	return fmt.Errorf("%s", codeholdCLIUsage)
}

type cliRolloutView struct {
	DryRun         bool            `json:"dry_run"`
	Update         bool            `json:"update"`
	Running        bool            `json:"running"`
	URL            string          `json:"url"`
	Concurrency    int             `json:"concurrency"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	Targets        []cliTargetView `json:"targets"`
	Skipped        []cliTargetView `json:"skipped"`
	Summary        map[string]int  `json:"summary"`
	Extra          map[string]any  `json:"-"`
}

type cliTargetView struct {
	WorkloadID string `json:"workload_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Plan       string `json:"plan"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	Done       bool   `json:"done"`
}

func parseCLIRollout(raw []byte) (*cliRolloutView, error) {
	var out struct {
		Rollout *cliRolloutView `json:"rollout"`
	}
	err := json.Unmarshal(raw, &out)
	return out.Rollout, err
}

func waitCodeholdCLIRollout(asJSON bool) error {
	last := -1
	for {
		raw, err := do("GET", "/api/v1/codehold-cli/rollout", nil, true)
		if err != nil {
			return err
		}
		r, err := parseCLIRollout(raw)
		if err != nil || r == nil {
			return fmt.Errorf("the rollout did not start")
		}
		done := 0
		for _, t := range r.Targets {
			if t.Done {
				done++
			}
		}
		if done != last && !asJSON {
			fmt.Fprintf(os.Stderr, "%d of %d containers done\n", done, len(r.Targets))
			last = done
		}
		if !r.Running {
			if asJSON {
				fmt.Println(string(raw))
			} else if err := printCLIRollout(r); err != nil {
				return err
			}
			if r.Summary["failed"] > 0 {
				return fmt.Errorf("%d container(s) failed", r.Summary["failed"])
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

func printCLIRollout(r *cliRolloutView) error {
	kind := "Install"
	if r.Update {
		kind = "Update"
	}
	if r.DryRun {
		fmt.Printf("Dry run (%s), nothing was changed. Source: %s\n\n", strings.ToLower(kind), r.URL)
	} else {
		fmt.Printf("%s from %s, %d at a time, %ds per container\n\n", kind, r.URL, r.Concurrency, r.TimeoutSeconds)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if r.DryRun {
		fmt.Fprintln(tw, "CONTAINER\tSTATUS\tCH NOW\tPLAN\tNOTE")
	} else {
		fmt.Fprintln(tw, "CONTAINER\tSTATUS\tRESULT\tVERSION\tREASON")
	}
	rows := append(append([]cliTargetView{}, r.Targets...), r.Skipped...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	for _, t := range rows {
		if r.DryRun {
			now := "not installed"
			if t.Installed {
				now = firstNonEmpty(t.Version, "installed")
			}
			plan := t.Plan
			if plan == "" {
				plan = "skip (" + strings.ReplaceAll(t.Outcome, "_", " ") + ")"
				now = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Status, now, plan, t.Reason)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Status, strings.ReplaceAll(firstNonEmpty(t.Outcome, "pending"), "_", " "), t.Version, t.Reason)
		}
	}
	_ = tw.Flush()
	if !r.DryRun {
		fmt.Printf("\nInstalled %d, updated %d, already current %d, failed %d", r.Summary["installed"], r.Summary["updated"], r.Summary["current"], r.Summary["failed"])
	} else {
		plans := map[string]int{}
		for _, t := range r.Targets {
			plans[t.Plan]++
		}
		fmt.Printf("\nWould install %d, update %d, skip %d (already installed)", plans["install"], plans["update"], plans["skip"])
		if plans["unknown"] > 0 {
			fmt.Printf(", could not check %d", plans["unknown"])
		}
	}
	fmt.Printf("; skipped %d not running, %d unsupported\n", r.Summary["not_running"], r.Summary["unsupported"])
	return nil
}
