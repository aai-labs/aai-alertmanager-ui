package config

import "testing"

func TestValidation(t *testing.T) {
	base := func() Config {
		return Config{ListenAddr: ":8080", Alertmanager: Source{URL: "http://alertmanager:9093"}}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing source", func(c *Config) { c.Alertmanager.URL = "" }},
		{"credentials in URL", func(c *Config) { c.Alertmanager.URL = "http://user:password@localhost" }},
		{"URL scheme", func(c *Config) { c.Alertmanager.URL = "file:///tmp/secret" }},
		{"unsafe base", func(c *Config) { c.BasePath = `/\"><script>` }},
		{"unknown category", func(c *Config) {
			c.Priorities = []Priority{{Name: "test", Match: map[string]string{"a": "b"}, Category: "Noise"}}
		}},
		{"no proxy trust", func(c *Config) { c.Tenancy = Tenancy{Enabled: true, IdentityHeader: "X-User"} }},
		{"invalid proxy", func(c *Config) {
			c.Tenancy = Tenancy{Enabled: true, IdentityHeader: "X-User", TrustedProxyCIDRs: []string{"proxy"}}
		}},
		{"wildcard grant", func(c *Config) {
			c.Tenancy = Tenancy{Enabled: true, IdentityHeader: "X-User", TrustedProxyCIDRs: []string{"127.0.0.1/32"}, Users: map[string][]string{"alice": {"*"}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := base()
			test.mutate(&c)
			if c.Validate() == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	c := base()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
