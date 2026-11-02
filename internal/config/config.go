// Package config loads optional deployment settings. Alertmanager is the only required source.
package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Source struct {
	URL             string     `yaml:"url"`
	BearerTokenFile string     `yaml:"bearerTokenFile"`
	BasicAuth       *BasicAuth `yaml:"basicAuth"`
	CAFile          string     `yaml:"caFile"`
}
type BasicAuth struct {
	Username     string `yaml:"username"`
	PasswordFile string `yaml:"passwordFile"`
}
type Priority struct {
	Name        string            `yaml:"name" json:"name"`
	Match       map[string]string `yaml:"match" json:"match"`
	Category    string            `yaml:"category" json:"category"`
	DisplayName string            `yaml:"displayName,omitempty" json:"displayName,omitempty"`
}
type Tenancy struct {
	Enabled           bool                `yaml:"enabled"`
	IdentityHeader    string              `yaml:"identityHeader"`
	GroupsHeader      string              `yaml:"groupsHeader"`
	TrustedProxyCIDRs []string            `yaml:"trustedProxyCIDRs"`
	Users             map[string][]string `yaml:"users"`
	Groups            map[string][]string `yaml:"groups"`
	OperatorUsers     []string            `yaml:"operatorUsers"`
	OperatorGroups    []string            `yaml:"operatorGroups"`
}
type Analytics struct {
	CoverageQuery string `yaml:"coverageQuery"`
}
type Enhanced struct {
	Enabled       bool   `yaml:"enabled"`
	DatabasePath  string `yaml:"databasePath"`
	RetentionDays int    `yaml:"retentionDays"`
}
type CatalogEntry struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description" json:"description"`
}
type Triage struct {
	Enabled          bool           `yaml:"enabled"`
	Apply            bool           `yaml:"apply"`
	APIKeyFile       string         `yaml:"apiKeyFile"`
	Model            string         `yaml:"model"`
	MonthlyBudgetUSD float64        `yaml:"monthlyBudgetUSD"`
	PolicyVersion    string         `yaml:"policyVersion"`
	Policy           string         `yaml:"policy"`
	Teams            []CatalogEntry `yaml:"teams"`
	Runbooks         []CatalogEntry `yaml:"runbooks"`
}
type FollowUp struct {
	URL             string `yaml:"url"`
	BearerTokenFile string `yaml:"bearerTokenFile"`
	PublicURL       string `yaml:"publicURL"`
}
type Config struct {
	Analytics    Analytics  `yaml:"analytics"`
	Enhanced     Enhanced   `yaml:"enhanced"`
	Triage       Triage     `yaml:"triage"`
	FollowUp     FollowUp   `yaml:"followUp"`
	Alertmanager Source     `yaml:"alertmanager"`
	Prometheus   Source     `yaml:"prometheus"`
	ListenAddr   string     `yaml:"listenAddr"`
	BasePath     string     `yaml:"basePath"`
	ReadOnly     bool       `yaml:"readOnly"`
	Demo         bool       `yaml:"demo"`
	Priorities   []Priority `yaml:"priorities"`
	Tenancy      Tenancy    `yaml:"tenancy"`
}

func Load() (Config, error) {
	c := Config{ListenAddr: ":8080"}
	c.Defaults()
	if name := os.Getenv("CONFIG_FILE"); name != "" {
		f, e := os.Open(name)
		if e != nil {
			return c, fmt.Errorf("open config: %w", e)
		}
		defer f.Close()
		d := yaml.NewDecoder(f)
		d.KnownFields(true)
		if e = d.Decode(&c); e != nil {
			return c, fmt.Errorf("parse config: %w", e)
		}
	}
	for key, dest := range map[string]*string{"ALERTMANAGER_URL": &c.Alertmanager.URL, "PROMETHEUS_URL": &c.Prometheus.URL, "LISTEN_ADDR": &c.ListenAddr, "BASE_PATH": &c.BasePath, "DATABASE_PATH": &c.Enhanced.DatabasePath} {
		if value, ok := os.LookupEnv(key); ok {
			*dest = value
		}
	}
	for key, dest := range map[string]*bool{"READ_ONLY": &c.ReadOnly, "DEMO": &c.Demo, "ENHANCED": &c.Enhanced.Enabled} {
		if value, ok := os.LookupEnv(key); ok {
			b, e := strconv.ParseBool(value)
			if e != nil {
				return c, fmt.Errorf("%s must be true or false", key)
			}
			*dest = b
		}
	}
	c.BasePath = strings.TrimRight(c.BasePath, "/")
	return c, c.Validate()
}
func (c *Config) Defaults() {
	if c.Enhanced.DatabasePath == "" {
		c.Enhanced.DatabasePath = "/data/alerts.db"
	}
	if c.Enhanced.RetentionDays == 0 {
		c.Enhanced.RetentionDays = 90
	}
	if c.Triage.Model == "" {
		c.Triage.Model = "typesafe/jev-1.13"
	}
	if c.Triage.PolicyVersion == "" {
		c.Triage.PolicyVersion = "1"
	}
	if c.Triage.MonthlyBudgetUSD == 0 {
		c.Triage.MonthlyBudgetUSD = 10
	}
}
func (c Config) Validate() error {
	if c.Enhanced.RetentionDays < 0 || c.Enhanced.RetentionDays > 3650 {
		return errors.New("enhanced retentionDays must be 1..3650")
	}
	if c.Triage.Enabled {
		if !c.Enhanced.Enabled || c.Demo {
			return errors.New("triage requires enhanced mode and cannot send demo data")
		}
		if c.Triage.APIKeyFile == "" {
			return errors.New("triage.apiKeyFile is required")
		}
		if c.Triage.Model != "" && c.Triage.Model != "typesafe/jev-1.13" && c.Triage.Model != "typesafe/jev-1.14" {
			return errors.New("triage.model must be a supported pinned Jev version")
		}
		if math.IsNaN(c.Triage.MonthlyBudgetUSD) || math.IsInf(c.Triage.MonthlyBudgetUSD, 0) || c.Triage.MonthlyBudgetUSD < 0 || c.Triage.MonthlyBudgetUSD > 1000 {
			return errors.New("triage budget must be positive and at most $1000")
		}
		if len(c.Triage.Policy) > 16000 || len(c.Triage.Teams) > 30 || len(c.Triage.Runbooks) > 30 {
			return errors.New("triage catalog or policy exceeds limit")
		}
		for _, entries := range [][]CatalogEntry{c.Triage.Teams, c.Triage.Runbooks} {
			seen := map[string]bool{}
			for _, e := range entries {
				if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(e.ID) || e.ID == "unknown" || seen[e.ID] || len(e.Description) > 2000 {
					return errors.New("invalid or duplicate triage catalog entry")
				}
				seen[e.ID] = true
			}
		}
	}
	if c.FollowUp.URL != "" {
		if !c.Enhanced.Enabled || c.Demo {
			return errors.New("followUp requires enhanced mode and cannot send demo events")
		}
		for _, value := range []string{c.FollowUp.URL, c.FollowUp.PublicURL} {
			u, e := url.Parse(value)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
				return errors.New("followUp URL and publicURL must be HTTPS without credentials or fragments")
			}
		}
		if c.FollowUp.BearerTokenFile == "" {
			return errors.New("followUp.bearerTokenFile is required")
		}
	}

	if !c.Demo && c.Alertmanager.URL == "" {
		return errors.New("ALERTMANAGER_URL or alertmanager.url is required (or set DEMO=true)")
	}
	if c.Demo && c.Tenancy.Enabled {
		return errors.New("demo and tenancy cannot be combined")
	}
	if c.BasePath != "" && (!strings.HasPrefix(c.BasePath, "/") || strings.ContainsAny(c.BasePath, "?#\\%") || strings.Contains(c.BasePath, "//") || strings.Contains(c.BasePath, "..") || !regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`).MatchString(c.BasePath)) {
		return errors.New("basePath must be a clean absolute URL path")
	}
	if c.ListenAddr == "" {
		return errors.New("listenAddr cannot be empty")
	}
	for name, s := range map[string]Source{"alertmanager": c.Alertmanager, "prometheus": c.Prometheus} {
		if s.URL != "" {
			u, e := url.Parse(s.URL)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("%s URL must be HTTP(S), without credentials, query or fragment", name)
			}
		}
		if s.BasicAuth != nil && (s.BasicAuth.Username == "" || s.BasicAuth.PasswordFile == "") {
			return fmt.Errorf("%s basicAuth requires username and passwordFile", name)
		}
		if s.BasicAuth != nil && s.BearerTokenFile != "" {
			return fmt.Errorf("%s cannot use basic and bearer auth together", name)
		}
	}
	categories := map[string]bool{"Core infrastructure": true, "Critical": true, "Availability": true, "Job failures": true, "Capacity": true, "Informational": true, "Other active alerts": true}
	for _, p := range c.Priorities {
		if p.Name == "" || len(p.Match) == 0 || !categories[p.Category] {
			return errors.New("each priority requires name, nonempty match and supported category")
		}
		for k := range p.Match {
			if k == "" {
				return errors.New("priority match label cannot be empty")
			}
		}
	}
	t := c.Tenancy
	if t.Enabled {
		if t.IdentityHeader == "" || len(t.TrustedProxyCIDRs) == 0 {
			return errors.New("tenancy requires identityHeader and trustedProxyCIDRs")
		}
		for _, h := range []string{t.IdentityHeader, t.GroupsHeader} {
			if h != "" && !regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$").MatchString(h) {
				return errors.New("invalid tenancy header")
			}
		}
		for _, cidr := range t.TrustedProxyCIDRs {
			if _, _, e := net.ParseCIDR(cidr); e != nil {
				return fmt.Errorf("invalid trusted proxy CIDR: %s", cidr)
			}
		}
		for _, grants := range []map[string][]string{t.Users, t.Groups} {
			for identity, namespaces := range grants {
				if strings.TrimSpace(identity) == "" {
					return errors.New("tenant identity cannot be empty")
				}
				for _, ns := range namespaces {
					if ns == "" || strings.ContainsAny(ns, "*, /\\\t\n") {
						return errors.New("tenant namespaces must be explicit nonempty names")
					}
				}
			}
		}
	}
	return nil
}
