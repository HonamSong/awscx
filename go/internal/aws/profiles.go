package aws

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AvailableProfiles returns unique profile names from ~/.aws/config and
// ~/.aws/credentials. config uses "[profile foo]" (bare "[default]"),
// credentials uses "[foo]". Mirrors boto3.Session().available_profiles.
func AvailableProfiles() []string {
	set := map[string]struct{}{}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	scanINISections(filepath.Join(home, ".aws", "config"), func(name string) {
		name = strings.TrimSpace(name)
		if name == "default" {
			set["default"] = struct{}{}
			return
		}
		if p, ok := strings.CutPrefix(name, "profile "); ok {
			set[strings.TrimSpace(p)] = struct{}{}
		}
	})
	scanINISections(filepath.Join(home, ".aws", "credentials"), func(name string) {
		set[strings.TrimSpace(name)] = struct{}{}
	})
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func scanINISections(path string, fn func(name string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) > 2 && line[0] == '[' && line[len(line)-1] == ']' {
			fn(line[1 : len(line)-1])
		}
	}
}