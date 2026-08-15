package config

import (
	"net"
	"strings"
)

// ResolvePublicOrigin 通知卡片可点击前缀。
// 配了 public_base 用配置；否则若监听绑了具体 IP 就用它；再否则探测本机局域网 IPv4。
func ResolvePublicOrigin(cfg *AppConfig) string {
	addr := ":7777"
	configured := ""
	if cfg != nil {
		if strings.TrimSpace(cfg.Server.Addr) != "" {
			addr = strings.TrimSpace(cfg.Server.Addr)
		}
		configured = strings.TrimSpace(cfg.Server.PublicBase)
	}
	if o := normalizeOrigin(configured); o != "" {
		return o
	}
	host, port := splitListen(addr)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = FirstLANIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "7777"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func normalizeOrigin(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "http://" + s
}

func splitListen(addr string) (host, port string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "7777"
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "", strings.TrimPrefix(addr, ":")
		}
		return addr, "7777"
	}
	return h, p
}

// FirstLANIPv4 本机第一块非回环、非链路本地的 IPv4（优先 RFC1918）
func FirstLANIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP == nil {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			s := ip.String()
			if ip.IsPrivate() {
				return s
			}
			if fallback == "" {
				fallback = s
			}
		}
	}
	return fallback
}
