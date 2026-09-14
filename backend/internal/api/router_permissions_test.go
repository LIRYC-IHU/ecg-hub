package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Each Connect service is mounted with a map of procedure → required
// permission. A procedure missing from its service's map used to be served with
// no permission check at all: adding an RPC and forgetting the entry published
// it to every authenticated caller, silently. The interceptors now refuse an
// unmapped procedure, and this test is what turns that refusal into a failure
// at `go test` rather than a broken endpoint someone finds later.
//
// The maps are literals inside RegisterRoutes, so they are read from the source
// rather than from a variable. That is deliberate: a test that read a shared
// registry would agree with itself if the registry stopped being the thing the
// router actually passes to the interceptor.
const routerSource = "router.go"

// procedureRef matches one map key: `apiv1connect.DeviceServiceListDevicesProcedure:`.
var procedureRef = regexp.MustCompile(`apiv1connect\.([A-Za-z0-9]+Service)([A-Za-z0-9]+)Procedure\s*:`)

// unprotectedServices are mounted without a permission map on purpose: they are
// reachable before a session exists, so there is no role to check.
//
// Listing them here rather than skipping any service that happens to be absent
// is the point — deleting a real service's map would otherwise make this test
// pass by removing what it was checking.
var unprotectedServices = map[string]string{
	"AuthService":     "login and provider discovery, before a session exists",
	"SetupService":    "first-run setup, before any user exists",
	"BrandingService": "logo and colours, read by the login screen",
	"SessionService":  "resolves the caller's own identity",
	"HealthzService":  "liveness probe",
}

func TestEveryProcedureHasAPermission(t *testing.T) {
	src, err := os.ReadFile(routerSource)
	if err != nil {
		t.Fatalf("reading %s: %v", routerSource, err)
	}

	mapped := map[string]map[string]bool{}
	for _, m := range procedureRef.FindAllStringSubmatch(string(src), -1) {
		svc, method := m[1], m[2]
		if mapped[svc] == nil {
			mapped[svc] = map[string]bool{}
		}
		mapped[svc][method] = true
	}
	if len(mapped) == 0 {
		t.Fatalf("no permission maps found in %s — the regex no longer matches the router", routerSource)
	}

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "grpc.api.v1" {
			return true
		}
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			name := string(svc.Name())

			if reason, ok := unprotectedServices[name]; ok {
				if len(mapped[name]) > 0 {
					t.Errorf("%s is listed as unprotected (%s) but the router maps permissions for it — "+
						"remove it from unprotectedServices", name, reason)
				}
				continue
			}
			if len(mapped[name]) == 0 {
				t.Errorf("%s is mounted with no permission map: every authenticated caller reaches all of it. "+
					"Add the map, or record it in unprotectedServices with the reason", name)
				continue
			}

			var missing []string
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				if m := string(methods.Get(j).Name()); !mapped[name][m] {
					missing = append(missing, m)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf("%s has no permission mapped for %s — the interceptor will refuse %s at runtime",
					name, strings.Join(missing, ", "), pluralise(len(missing)))
			}
		}
		return true
	})
}

func pluralise(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
