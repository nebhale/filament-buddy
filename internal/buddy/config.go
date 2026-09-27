package buddy

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var printerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,8}$`)

type Printer struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	SourceIP string `yaml:"source_ip"`
}
type Config struct {
	HTTP struct {
		Address string `yaml:"address"`
	} `yaml:"http"`
	Metrics struct {
		Address        string `yaml:"address"`
		AdvertisedHost string `yaml:"advertised_host"`
		AdvertisedPort int    `yaml:"advertised_port"`
	} `yaml:"metrics"`
	Spoolman struct {
		URL     string        `yaml:"url"`
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"spoolman"`
	Printers     []Printer `yaml:"printers"`
	DataDir      string    `yaml:"data_dir"`
	AuthUser     string    `yaml:"-"`
	AuthPassword string    `yaml:"-"`
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c, err := DecodeConfig(f)
	if err != nil {
		return c, err
	}
	c.AuthUser, c.AuthPassword = os.Getenv("FILAMENT_BUDDY_AUTH_USER"), os.Getenv("FILAMENT_BUDDY_AUTH_PASSWORD")
	if (c.AuthUser == "") != (c.AuthPassword == "") {
		return c, errors.New("both authentication variables must be set together")
	}
	if v := os.Getenv("FILAMENT_BUDDY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	return c, nil
}
func DecodeConfig(r io.Reader) (Config, error) {
	var c Config
	c.HTTP.Address = ":8080"
	c.Metrics.Address = ":8514"
	c.DataDir = "/data"
	c.Spoolman.Timeout = 5 * time.Second
	d := yaml.NewDecoder(r)
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("expected one YAML document")
	}
	for _, addr := range []string{c.HTTP.Address, c.Metrics.Address} {
		_, p, err := net.SplitHostPort(addr)
		n, e := strconv.Atoi(p)
		if err != nil || e != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid listen address %q", addr)
		}
	}
	if c.Metrics.AdvertisedPort == 0 {
		_, p, _ := net.SplitHostPort(c.Metrics.Address)
		c.Metrics.AdvertisedPort, _ = strconv.Atoi(p)
	}
	if c.Metrics.AdvertisedPort < 1 || c.Metrics.AdvertisedPort > 65535 {
		return c, errors.New("advertised_port must be 1–65535")
	}
	if _, err := netip.ParseAddr(c.Metrics.AdvertisedHost); err != nil {
		return c, errors.New("advertised_host must be a LAN IP")
	}
	u, err := url.Parse(c.Spoolman.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("spoolman.url must be an HTTP(S) base URL")
	}
	if c.Spoolman.Timeout < 100*time.Millisecond || c.Spoolman.Timeout > time.Minute {
		return c, errors.New("spoolman.timeout must be 100ms–1m")
	}
	if strings.TrimSpace(c.DataDir) == "" || len(c.Printers) == 0 {
		return c, errors.New("data_dir and at least one printer are required")
	}
	seen := map[string]bool{}
	for i := range c.Printers {
		p := &c.Printers[i]
		if !printerIDPattern.MatchString(p.ID) || seen[p.ID] {
			return c, fmt.Errorf("invalid or duplicate printer ID %q (1–9 lowercase letters, digits, hyphens or underscores)", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" {
			p.Name = p.ID
		}
		if p.SourceIP != "" {
			if _, err := netip.ParseAddr(p.SourceIP); err != nil {
				return c, errors.New("source_ip must be the observed sender IP")
			}
		}
	}
	return c, nil
}
