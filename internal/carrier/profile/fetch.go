package profile

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultBaseURL 是画像仓库的默认根 URL（GitHub Raw）。
const DefaultBaseURL = "https://raw.githubusercontent.com/voorz/ios-carrier-profiles/main"

// DefaultFallbackURLs 是拉取失败时的备用 CDN 地址（按顺序尝试）。
var DefaultFallbackURLs = []string{
	"https://cdn.jsdelivr.net/gh/voorz/ios-carrier-profiles@main",
}

// NewFetcher 创建 Fetcher。cacheDir 为空则用 os.UserCacheDir 下的 ims-go/profiles。
func NewFetcher(baseURL, cacheDir string, log *slog.Logger) *Fetcher {
	return NewFetcherWithFallback(baseURL, cacheDir, DefaultFallbackURLs, log)
}

// NewFetcherWithFallback 创建带自定义备用地址的 Fetcher。
// fallbackURLs 传 nil 用默认；传空 slice 禁用 fallback。
func NewFetcherWithFallback(baseURL, cacheDir string, fallbackURLs []string, log *slog.Logger) *Fetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if fallbackURLs == nil {
		fallbackURLs = DefaultFallbackURLs
	}
	if cacheDir == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(dir, "ims-go", "profiles")
		} else {
			cacheDir = filepath.Join(os.TempDir(), "ims-go-profiles")
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Fetcher{
		baseURL:      baseURL,
		fallbackURLs: fallbackURLs,
		cacheDir:     cacheDir,
		client:       &http.Client{Timeout: 15 * time.Second},
		log:          log,
		manifest:     make(map[string]*SelectorManifest),
		profiles:     make(map[string]*Profile),
	}
}

// FetchProfile 按 PLMN + SIM 身份拉取画像（走 selector 匹配）。
// 返回命中的 Profile；无匹配时返回 nil, nil。
func (f *Fetcher) FetchProfile(plmn string, sim SIMIdentity) (*Profile, error) {
	manifest, err := f.fetchManifest(plmn)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, nil // 无 selector（未知 PLMN）
	}
	path := matchSelector(manifest, sim)
	if path == "" {
		return nil, nil // 有 manifest 但无 SIM 匹配
	}
	return f.fetchProfile(path)
}

// fetchManifest 拉取 selectors/<plmn>.yaml。
func (f *Fetcher) fetchManifest(plmn string) (*SelectorManifest, error) {
	f.mu.Lock()
	if m, ok := f.manifest[plmn]; ok {
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()

	rel := fmt.Sprintf("selectors/%s.yaml", plmn)
	data, err := f.fetch(rel)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var m SelectorManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 manifest %s: %w", rel, err)
	}
	f.mu.Lock()
	f.manifest[plmn] = &m
	f.mu.Unlock()
	return &m, nil
}

// fetchProfile 拉取 profiles/<slug>.yaml。
func (f *Fetcher) fetchProfile(path string) (*Profile, error) {
	f.mu.Lock()
	if p, ok := f.profiles[path]; ok {
		f.mu.Unlock()
		return p, nil
	}
	f.mu.Unlock()

	data, err := f.fetch(path)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("画像 %s 不存在", path)
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("解析画像 %s: %w", path, err)
	}
	f.mu.Lock()
	f.profiles[path] = &p
	f.mu.Unlock()
	return &p, nil
}

// fetch 先查本地磁盘缓存，未命中再从云端拉取并写入缓存。
// 云端 404 返回 (nil, nil)；主地址失败自动尝试 fallback CDN。
// 全部失败且无缓存时返回错误。
func (f *Fetcher) fetch(rel string) ([]byte, error) {
	cachePath := filepath.Join(f.cacheDir, rel)
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	urls := append([]string{f.baseURL}, f.fallbackURLs...)
	var lastErr error
	for i, base := range urls {
		url := base + "/" + rel
		data, err := f.fetchOne(url)
		if err == nil {
			if i > 0 {
				f.log.Info("画像拉取切换到备用地址", "url", base)
			}
			// 写入缓存（失败不致命）
			if mkErr := os.MkdirAll(filepath.Dir(cachePath), 0o755); mkErr == nil {
				_ = os.WriteFile(cachePath, data, 0o644)
			}
			return data, nil
		}
		if isNotFound(err) {
			return nil, nil
		}
		lastErr = err
		f.log.Warn("画像拉取失败，尝试下一个地址", "url", url, "err", err)
	}
	return nil, fmt.Errorf("拉取 %s 全部失败: %w", rel, lastErr)
}

// fetchOne 从单个 URL 拉取。404 返回 notFoundError。
func (f *Fetcher) fetchOne(url string) ([]byte, error) {
	resp, err := f.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, notFoundError{url}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// notFoundError 表示 404（该 PLMN 无画像，非网络故障，不触发 fallback）。
type notFoundError struct{ url string }

func (e notFoundError) Error() string { return "404: " + e.url }

func isNotFound(err error) bool {
	_, ok := err.(notFoundError)
	return ok
}

// matchSelector 在 manifest 中匹配 SIM 身份，返回命中的 bundle path。
func matchSelector(m *SelectorManifest, sim SIMIdentity) string {
	for _, p := range m.Profiles {
		for _, sel := range p.Selectors {
			if matchConditions(sel.Conditions, sim) {
				return p.Path
			}
		}
	}
	return ""
}

// matchConditions 检查所有条件是否满足。
func matchConditions(conds []Condition, sim SIMIdentity) bool {
	for _, c := range conds {
		var field string
		switch c.Field {
		case "gid1":
			field = sim.GID1
		case "gid2":
			field = sim.GID2
		case "iccid":
			field = sim.ICCID
		case "imsi":
			field = sim.IMSI
		default:
			return false
		}
		switch c.Match {
		case "prefix":
			if len(field) < len(c.Value) || field[:len(c.Value)] != c.Value {
				return false
			}
		case "exact":
			if field != c.Value {
				return false
			}
		default:
			return false
		}
	}
	return true
}
