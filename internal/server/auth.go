package server

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

type access struct {
	identity   string
	namespaces map[string]bool
	operator   bool
}

func (a access) allowed(labels map[string]string) bool {
	return a.operator || (labels["namespace"] != "" && a.namespaces[labels["namespace"]])
}
func (a access) namespaceList() []string {
	if a.operator {
		return nil
	}
	result := make([]string, 0, len(a.namespaces))
	for ns := range a.namespaces {
		result = append(result, ns)
	}
	sort.Strings(result)
	return result
}
func (s *Server) authorize(r *http.Request) (access, bool) {
	t := s.config.Tenancy
	if !t.Enabled {
		return access{identity: "operator", operator: true}, true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return access{}, false
	}
	ip := net.ParseIP(host)
	trusted := false
	for _, cidr := range t.TrustedProxyCIDRs {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			trusted = true
			break
		}
	}
	if !trusted {
		return access{}, false
	}
	if len(r.Header.Values(t.IdentityHeader)) != 1 {
		return access{}, false
	}
	identity := strings.TrimSpace(r.Header.Get(t.IdentityHeader))
	if identity == "" || strings.ContainsAny(identity, "\r\n,") {
		return access{}, false
	}
	a := access{identity: identity, namespaces: map[string]bool{}}
	for _, user := range t.OperatorUsers {
		if user == identity {
			a.operator = true
			return a, true
		}
	}
	for _, ns := range t.Users[identity] {
		a.namespaces[ns] = true
	}
	if t.GroupsHeader != "" {
		for _, group := range strings.Split(r.Header.Get(t.GroupsHeader), ",") {
			group = strings.TrimSpace(group)
			for _, g := range t.OperatorGroups {
				if g == group && group != "" {
					a.operator = true
					return a, true
				}
			}
			for _, ns := range t.Groups[group] {
				a.namespaces[ns] = true
			}
		}
	}
	return a, len(a.namespaces) > 0
}
