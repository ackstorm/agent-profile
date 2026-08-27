//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/hydrate"
	"github.com/ackstorm/agent-profile/pkg/schema"
)

// cmdInstall installs ONE capability into a root, with no manifest anywhere on
// disk (§35.1).
//
// It builds a one-resource profile in memory and hands it to the same
// resolution and the same Apply a manifest goes through. Nothing here is a
// second materialization path: an install that wrote files a different way
// would record them a different way, and the ledger is the only state there is.
//
// The first argument is the subject, as everywhere except render: the reference
// names the agent and the root, then the kind and the name say what.
func cmdInstall(args []string) error {
	const use = "install <agent>:<profile>|<agent> --root <dir> <kind> <name> [--git <url> [--ref <r>] [--subpath <p>] | --local <path> | --url <url> --digest <sha256:...>] [--dest <d>] [--auth-secret-env <VAR> | --auth-secret-file <path>] [--yes]"
	fs := flagSet("install")
	git := fs.String("git", "", "git repository to install from")
	gitRef := fs.String("ref", "", "branch, tag or commit for --git")
	subpath := fs.String("subpath", "", "path inside the source to install")
	local := fs.String("local", "", "local directory to install from")
	archiveURL := fs.String("url", "", "archive to install from; requires --digest")
	digest := fs.String("digest", "", `expected archive digest, "sha256:" and 64 hex characters`)
	dest := fs.String("dest", "", "destination for an artifact (§26.1)")
	secretEnv := fs.String("auth-secret-env", "", "environment variable holding the source credential")
	secretFile := fs.String("auth-secret-file", "", "file holding the source credential")
	strict := fs.Bool("strict", false, "promote every degradation warning (§7.2, §8) to an error")
	rootFlag := fs.String("root", "", "install into this directory instead of a profile's (§33.2)")
	yes := fs.Bool("yes", false, "install into the agent's real configuration without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")

	stop, pos, err := parsePositionals(fs, args, use, 3)
	if stop {
		return err
	}
	if len(pos) != 3 {
		return fmt.Errorf("usage: ap %s", use)
	}
	tgt, err := resolveTarget(pos[0], *rootFlag, "install")
	if err != nil {
		return err
	}
	kind, resource := pos[1], pos[2]
	if err := checkInstallKind(kind, resource); err != nil {
		return err
	}

	binding, err := authBinding(*secretEnv, *secretFile)
	if err != nil {
		return err
	}
	if err := tgt.gate(*yes); err != nil {
		return err
	}

	prof, base, err := oneResourceProfile(tgt.Root, tgt.Agent.Name, kind, resource, locator{
		git: *git, gitRef: *gitRef, subpath: *subpath,
		local: *local, url: *archiveURL, digest: *digest, dest: *dest,
	}, binding)
	if err != nil {
		return err
	}

	res, resolved, err := schema.ResolveProfile(prof, tgt.Agent.Name, tgt.Name != "")
	if err != nil {
		return err
	}
	if *strict && len(res.Warnings) > 0 {
		return fmt.Errorf("strict: %s", res.Warnings[0].Text)
	}
	res, _, fetched, reports, err := fetchPhase(res, resolved, base)
	if err != nil {
		return err
	}

	adapter, err := hydrate.AdapterFor(tgt.Agent)
	if err != nil {
		return err
	}
	applied, err := hydrate.Apply(context.Background(), hydrate.Plan{
		Root: tgt.Root, Adapter: adapter, Profile: res.Profile, Fetched: fetched,
	})
	if err != nil {
		return err
	}
	return printApplied(os.Stdout, res, tgt, applied, reports)
}

// checkInstallKind refuses, by name, the two things v1 deliberately does not do
// here (§35.1 as amended, and the command surface's own scope note).
func checkInstallKind(kind, resource string) error {
	switch kind {
	case "skill", "plugin", "artifact":
	case "mcp", "model":
		return fmt.Errorf("%s has no source to install from: declare it in a manifest and run `ap manifest apply`", kind)
	default:
		return fmt.Errorf("unknown kind %q: install takes skill, plugin or artifact", kind)
	}
	if strings.Contains(resource, "@") {
		// v1 keeps no record of a marketplace definition (§33.1), so there is
		// nothing on the root to resolve the catalogue name against. Refused
		// by name rather than resolved against a guess.
		item, market, _ := strings.Cut(resource, "@")
		return fmt.Errorf(
			"%q is a marketplace ref, and v1 records no marketplace definition to resolve %q against; "+
				"declare the marketplace and the item %q in a manifest and run `ap manifest apply`",
			resource, market, item)
	}
	return nil
}

type locator struct{ git, gitRef, subpath, local, url, digest, dest string }

// source builds §16's union from the flags, and returns the directory a
// manifest declaring it would have lived in.
//
// It refuses more than one arm for the same reason the schema does: two sources
// for one resource has no meaning, and picking one silently is how the wrong
// content gets installed.
//
// --local is the interesting one. §16 requires a local path to stay inside the
// manifest's directory, and an install has no manifest — so the path's own
// PARENT is that directory and the path becomes its base name. The containment
// rule is then satisfied by construction rather than waived, which matters
// because it is the rule that stops a manifest from a repository reading
// ~/.ssh: relaxing it here would relax it for the shared validator.
func (l locator) source(auth *schema.GitAuth) (*schema.Source, string, error) {
	var declared []string
	for _, f := range []struct {
		flag string
		set  bool
	}{{"--git", l.git != ""}, {"--local", l.local != ""}, {"--url", l.url != ""}} {
		if f.set {
			declared = append(declared, f.flag)
		}
	}
	switch len(declared) {
	case 0:
		return nil, "", fmt.Errorf("an install needs a source: --git, --local or --url")
	case 1:
	default:
		return nil, "", fmt.Errorf("name the source once: %s and %s were both given", declared[0], declared[1])
	}

	// A source with no local path never reads the filesystem relative to
	// anything, so the shell's directory is as good an answer as any.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}

	switch {
	case l.git != "":
		return &schema.Source{Git: &schema.GitSource{
			URL: l.git, Ref: l.gitRef, Subpath: l.subpath, Auth: auth,
		}}, cwd, nil
	case l.local != "":
		if auth != nil {
			return nil, "", fmt.Errorf("--local reads the filesystem and takes no credential")
		}
		abs, err := filepath.Abs(l.local)
		if err != nil {
			return nil, "", err
		}
		return &schema.Source{Local: &schema.LocalSource{
			Path: filepath.Base(abs), Subpath: l.subpath,
		}}, filepath.Dir(abs), nil
	default:
		if l.digest == "" {
			return nil, "", fmt.Errorf("--url requires --digest: an archive is verified before it is extracted (§18)")
		}
		return &schema.Source{Archive: &schema.ArchiveSource{
			URL: l.url, Digest: l.digest, Subpath: l.subpath, Auth: auth,
		}}, cwd, nil
	}
}

// authBinding turns the two flags into §13.1's binding. The VALUE is never
// taken here and never recorded — only where to find it (§34).
func authBinding(env, file string) (*schema.Binding, error) {
	switch {
	case env != "" && file != "":
		return nil, fmt.Errorf("name the credential once: --auth-secret-env and --auth-secret-file were both given")
	case env != "":
		return &schema.Binding{Env: env}, nil
	case file != "":
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		return &schema.Binding{File: abs}, nil
	}
	return nil, nil
}

// oneResourceProfile is the manifest an install does not have. It returns the
// directory that manifest would have lived in, which is what §16's containment
// rule for a local source is measured against.
func oneResourceProfile(root, runtime, kind, name string, l locator, binding *schema.Binding) (schema.Profile, string, error) {
	p := schema.Profile{Version: "1", Name: name, Targets: []string{runtime}}

	var auth *schema.GitAuth
	if binding != nil {
		ledger, err := hydrate.LoadLedger(root)
		if err != nil {
			return p, "", err
		}
		// The logical name is derived HERE, against the ledger, and recorded —
		// not at export time. §35.2 requires the derivation to be stable, and
		// the ledger is the only state that can make it so: two installs
		// naming one variable get one input, and two naming different ones
		// cannot collide into a single entry that authenticates the wrong
		// source.
		secret := deriveSecretName(ledger, *binding)
		p.Inputs.Secrets = map[string]schema.Binding{secret: *binding}
		auth = &schema.GitAuth{ValueFrom: schema.ValueFrom{Secret: secret}}
	}

	src, base, err := l.source(auth)
	if err != nil {
		return p, "", err
	}
	switch kind {
	case "skill":
		p.Skills = map[string]schema.Resource{name: {Enabled: true, Source: src}}
	case "plugin":
		p.Plugins = map[string]schema.Resource{name: {Enabled: true, Source: src}}
	case "artifact":
		if l.dest == "" {
			return p, "", fmt.Errorf("an artifact needs --dest: it names where the content lands (§26.1)")
		}
		p.Artifacts = map[string]schema.Artifact{name: {Enabled: true, Source: src, Destination: l.dest}}
	}
	return p, base, nil
}

// deriveSecretName gives a nameless binding a stable logical name.
//
// An identical binding already in the ledger keeps its name, so installing two
// skills from one private repository declares one input rather than two. A
// DIFFERENT binding that derives the same name takes the first free suffix —
// two files called token.txt in two directories are two credentials, and
// merging them would send one repository's token to the other.
func deriveSecretName(l *hydrate.Ledger, b schema.Binding) string {
	taken := map[string]schema.Binding{}
	for _, rec := range l.Resources {
		for n, existing := range rec.Secrets {
			if existing == b {
				return n
			}
			taken[n] = existing
		}
	}
	base := kebab(b.Env)
	if b.File != "" {
		base = kebab(strings.TrimSuffix(filepath.Base(b.File), filepath.Ext(b.File)))
	}
	if base == "" {
		base = "secret"
	}
	candidate := base
	for i := 2; ; i++ {
		if _, clash := taken[candidate]; !clash {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// kebab lowercases and turns anything a manifest name may not carry into a
// hyphen, so GITLAB_TOKEN becomes gitlab-token and the exported manifest reads
// like one somebody wrote.
func kebab(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
