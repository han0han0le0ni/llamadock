// Package netinfo 枚举本机网卡、自动挑选手机可访问的局域网地址，并组装接入信息
// （含二维码 payload 与各语言调用示例）。
//
// 选路策略（避免选中 ZeroTier / VMware / Hyper-V 等虚拟网卡）：
//  1. 排除回环地址；
//  2. 排除 169.254.x.x 链路本地地址（未获取到 DHCP，手机连不上）；
//  3. 对名称命中虚拟 / VPN 关键字的网卡大幅降权；
//  4. 对真实「无线 / 有线」网卡加分；
//  5. 综合得分排序，取最高的「可用」网卡。
package netinfo

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// Iface 一张网卡。
type Iface struct {
	Name        string `json:"name"`
	IP          string `json:"ip"`
	Private     bool   `json:"private"`
	Loopback    bool   `json:"loopback"`
	Virtual     bool   `json:"virtual"`     // 虚拟 / VPN / 隧道网卡（ZeroTier、VMware、Hyper-V…）
	LinkLocal   bool   `json:"link_local"`  // 169.254.x.x 自动私有地址
	Usable      bool   `json:"usable"`      // 手机是否有可能连得上
	Recommended bool   `json:"recommended"` // 是否为程序自动推荐的首选网卡
	Score       int    `json:"score"`       // 排序得分（便于调试）
	Reason      string `json:"reason"`      // 判定说明
}

// Access 给客户端用的接入信息。
type Access struct {
	BaseURL    string  `json:"base_url"` // http://192.168.x.x:8080/v1
	Host       string  `json:"host"`     // http://192.168.x.x:8080
	APIKey     string  `json:"api_key"`  // 空字符串表示无需填写
	NoKeyHint  string  `json:"no_key_hint"`
	Interfaces []Iface `json:"interfaces"`
	Curl       string  `json:"curl"`
	Python     string  `json:"python"`
	Node       string  `json:"node"`
	Tip        string  `json:"tip"`
}

// 虚拟 / VPN / 隧道网卡关键字（命中即降权）。
var virtualHints = []string{
	"zerotier", "zt", "vmware", "vmnet", "virtualbox", "vbox", "hyper-v",
	"vethernet", "docker", "br-", "veth", "wsl", "tailscale", "utun", "tun",
	"tap", "radmin", "hamachi", "bluetooth", "蓝牙", "teredo", "isatap",
	"6to4", "openvpn", "wireguard", "nordlynx", "npcap", "vpn", "tunnel",
	"virtual", "loopback", "pseudo", "clash", "mihomo",
}

// 真实无线网卡关键字（优先）。
var wifiHints = []string{"wlan", "wi-fi", "wifi", "wireless", "无线", "802.11"}

// 真实有线网卡关键字。
var lanHints = []string{"以太网", "ethernet", "eth0", "eth1", "eth2", "enp", "eno", "ens", "en0"}

func matchesAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// analyze 给单张网卡打分并给出判定说明。
func analyze(name string, ip net.IP, flags net.Flags) (score int, virtual, linkLocal, usable bool, reason string) {
	lname := strings.ToLower(name)
	linkLocal = ip[0] == 169 && ip[1] == 254
	virtual = matchesAny(lname, virtualHints) || ip.IsLoopback()

	// 回环与链路本地地址直接判定为不可用。
	if ip.IsLoopback() {
		return -1000, true, false, false, "回环地址"
	}
	if linkLocal {
		score -= 800
		reason = "169.254 链路本地地址（未获取到网络）"
	}
	if matchesAny(lname, virtualHints) {
		score -= 400
		reason = "虚拟 / VPN 网卡"
	}

	// 真实物理网卡加分。
	if !virtual {
		if matchesAny(lname, wifiHints) {
			score += 120
			reason = "无线网卡"
		} else if matchesAny(lname, lanHints) {
			score += 90
			reason = "有线网卡"
		}
	}

	if ip.IsPrivate() {
		score += 40
	} else {
		score -= 60
	}
	// 家庭 / 办公常见网段微调。
	if ip[0] == 192 && ip[1] == 168 {
		score += 15
	} else if ip[0] == 10 {
		score += 5
	}
	if flags&net.FlagBroadcast != 0 {
		score += 5
	}
	if flags&net.FlagPointToPoint != 0 {
		score -= 40
	}

	usable = !ip.IsLoopback() && !linkLocal && !virtual
	if reason == "" {
		reason = "其他网卡"
	}
	return
}

// List 枚举本机 IPv4 地址，按「手机可连性」从优到劣排序。
func List() []Iface {
	out := []Iface{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			score, virt, ll, usable, reason := analyze(ifc.Name, ip, ifc.Flags)
			out = append(out, Iface{
				Name:      ifc.Name,
				IP:        ip.String(),
				Private:   ip.IsPrivate(),
				Loopback:  ip.IsLoopback(),
				Virtual:   virt,
				LinkLocal: ll,
				Usable:    usable,
				Score:     score,
				Reason:    reason,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].IP < out[j].IP
	})
	// 标记推荐项：得分最高的「可用」网卡。
	for k := range out {
		if out[k].Usable {
			out[k].Recommended = true
			break
		}
	}
	return out
}

// pickIP 从已排序的网卡列表中挑出最适合手机连接的地址。
func pickIP(list []Iface) string {
	for _, i := range list {
		if i.Usable {
			return i.IP
		}
	}
	// 没有理想网卡时，退而求其次（排除回环与链路本地）。
	for _, i := range list {
		if !i.Loopback && !i.LinkLocal {
			return i.IP
		}
	}
	return "127.0.0.1"
}

// BestLANIP 挑一个最适合手机连接的地址。
func BestLANIP() string {
	return pickIP(List())
}

// Build 组装接入信息。
func Build(port int, model string) Access {
	list := List()
	ip := pickIP(list)
	host := fmt.Sprintf("http://%s:%d", ip, port)
	base := host + "/v1"
	if model == "" {
		model = "模型名"
	}

	tip := "手机 / 笔记本在同一 WiFi 下，只填 Base URL 即可直接用；模型未加载时会自动加载。"
	usable := false
	for _, i := range list {
		if i.Usable {
			usable = true
			break
		}
	}
	if !usable {
		tip = "提示：未检测到可用的局域网 / WiFi 网卡（当前地址 " + ip + "），手机可能连不上。请确认本机已连接 WiFi 或网线。"
	}

	return Access{
		BaseURL:    base,
		Host:       host,
		APIKey:     "",
		NoKeyHint:  "API Key 留空即可（本程序默认不校验）",
		Interfaces: list,
		Curl:       fmt.Sprintf(`curl %s/chat/completions -H "Content-Type: application/json" -d "{\"model\":\"%s\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}"`, base, model),
		Python: fmt.Sprintf(`from openai import OpenAI
client = OpenAI(base_url="%s", api_key="not-needed")
r = client.chat.completions.create(model="%s", messages=[{"role":"user","content":"你好"}])
print(r.choices[0].message.content)`, base, model),
		Node: fmt.Sprintf(`// OpenAI 兼容客户端，baseURL = "%s"
import OpenAI from "openai";
const client = new OpenAI({ baseURL: "%s", apiKey: "not-needed" });
const r = await client.chat.completions.create({ model: "%s", messages: [{ role: "user", content: "你好" }] });
console.log(r.choices[0].message.content);`, base, base, model),
		Tip: tip,
	}
}

// Hostname 本机名。
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h)
}
