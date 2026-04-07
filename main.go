package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/BalanceBalls/nekot/clients"
	"github.com/BalanceBalls/nekot/config"
	"github.com/BalanceBalls/nekot/migrations"
	"github.com/BalanceBalls/nekot/util"
	"github.com/BalanceBalls/nekot/views"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

const ctxTime = 2000
const chatbangDebugPort = 19222

// a list of all possible common executable names
// for chromium-based browsers.
var browsers = []string{
	"chromium",
	"chromium-browser",
	"google-chrome",
	"google-chrome-stable",
	"microsoft-edge",
	"microsoft-edge-stable",
	"brave-browser",
	"vivaldi",
	"opera",
	"msedge",
	"ungoogled-chromium",
}

func detectBrowser() (string, error) {
	if goruntime.GOOS == "darwin" {
		// macOS: check for Chrome/Chromium .app bundles
		macApps := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
		}
		for _, path := range macApps {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	var basePaths = []string{
		"/bin/",
		"/usr/bin/",
	}
	for _, basePath := range basePaths {
		for _, name := range browsers {
			path := basePath + name
			if _, err := os.Stat(path); err == nil {
				fmt.Println(path)
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("no Chromium-based browser found in PATH")
}

/** resolveAppName extracts the .app bundle name from a macOS binary path. */
func resolveAppName(browserPath string) string {
	if idx := strings.Index(browserPath, ".app/"); idx != -1 {
		prefix := browserPath[:idx]
		if lastSlash := strings.LastIndex(prefix, "/"); lastSlash != -1 {
			return prefix[lastSlash+1:]
		}
		return prefix
	}
	return browserPath
}

/** isPortOpen checks if a TCP port is accepting connections. */
func isPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

/** connectCDP connects to an already-running Chrome via CDP. */
func connectCDP(debugPort int) (context.Context, context.CancelFunc) {
	devtoolsURL := fmt.Sprintf("http://127.0.0.1:%d", debugPort)
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), devtoolsURL)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	cancel := func() {
		ctxCancel()
		allocCancel()
	}
	return ctx, cancel
}

/** launchChromeViaCDP launches Chrome via macOS `open -na` and connects via CDP.
    If Chrome is already running on the debug port, it reuses the existing instance. */
func launchChromeViaCDP(browserPath string, profileDir string, debugPort int) (context.Context, context.CancelFunc, error) {
	freshLaunch := !isPortOpen(debugPort)

	if freshLaunch {
		appName := resolveAppName(browserPath)
		out, err := exec.Command("open", "-na", appName, "--args",
			fmt.Sprintf("--remote-debugging-port=%d", debugPort),
			fmt.Sprintf("--user-data-dir=%s", profileDir),
			"--no-first-run",
			"--no-startup-window",
		).CombinedOutput()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to launch Chrome via open: %w (output: %s)", err, string(out))
		}

		// Wait for debug port
		for i := 0; i < 60; i++ {
			if isPortOpen(debugPort) {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !isPortOpen(debugPort) {
			return nil, nil, fmt.Errorf("Chrome debug port %d not reachable after 18s", debugPort)
		}
	}

	ctx, cancel := connectCDP(debugPort)
	return ctx, cancel, nil
}

func main() {
	usr, err := user.Current()
	if err != nil {
		fmt.Println("Error fetching user info:", err)
		return
	}

	configDir := usr.HomeDir + "/.config/chatbang"
	configPath := configDir + "/chatbang"
	profileDir := usr.HomeDir + "/.config/chatbang/profile_data"

	err = os.MkdirAll(configDir, 0o755)
	if err != nil {
		fmt.Println("Error creating config directory:", err)
		return
	}

	configFile, err := os.OpenFile(configPath,
		os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Println("Error opening config file:", err)
		return
	}
	defer configFile.Close()

	info, err := configFile.Stat()
	if err != nil {
		fmt.Println("Error getting file info:", err)
		return
	}

	if info.Size() == 0 {
		configFile.Seek(0, 0)
	}

	// read browser from config
	var defaultBrowser string
	scanner := bufio.NewScanner(configFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "browser" {
			defaultBrowser = strings.TrimSpace(parts[1])
		}
	}

	// Step 2: if config is empty or invalid, detect in PATH
	if defaultBrowser == "" {
		detectedBrowser, err := detectBrowser()
		if err != nil {
			fmt.Println("No Chromium-based browser found in PATH or config.")
			fmt.Println("Please install a Chromium-based browser or edit the config at", configPath)
			return
		}

		defaultBrowser = detectedBrowser
		defaultbrowserConfig := "browser=" + defaultBrowser

		_, err = io.WriteString(configFile, defaultbrowserConfig)
		if err != nil {
			fmt.Println("Error writing default config:", err)
			return
		}
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "--config" {
			loginProfile(defaultBrowser, profileDir)
			return
		}

		if os.Args[1] == "--help" || os.Args[1] == "-h" {
			fmt.Println("Chatbang - Access ChatGPT from the terminal without an API key")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  chatbang          Start chatting (Chrome must be running from --config)")
			fmt.Println("  chatbang --config  One-time login setup (opens Chrome, log in to ChatGPT)")
			fmt.Println("  chatbang --help    Show this help")
			return
		}
	}

	var taskCtx context.Context
	var taskCancel context.CancelFunc

	if goruntime.GOOS == "darwin" {
		ctx, cancel, err := launchChromeViaCDP(defaultBrowser, profileDir, chatbangDebugPort)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		taskCtx, taskCancel = context.WithTimeout(ctx, ctxTime*time.Second)
		defer taskCancel()
	} else {
		allocatorCtx, cancel := chromedp.NewExecAllocator(context.Background(),
			append(chromedp.DefaultExecAllocatorOptions[:],
				chromedp.ExecPath(defaultBrowser),
				chromedp.Flag("disable-blink-features", "AutomationControlled"),
				chromedp.Flag("exclude-switches", "enable-automation"),
				chromedp.Flag("disable-extensions", false),
				chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
				chromedp.Flag("disable-default-apps", false),
				chromedp.Flag("disable-dev-shm-usage", false),
				chromedp.Flag("disable-gpu", false),
				chromedp.UserDataDir(profileDir),
				chromedp.Flag("profile-directory", "Default"),
			)...,
		)
		defer cancel()

		ctx, cancel := chromedp.NewContext(allocatorCtx)
		defer cancel()

		taskCtx, taskCancel = context.WithTimeout(ctx, ctxTime*time.Second)
		defer taskCancel()
	}

	// Install clipboard interceptor BEFORE navigating so it runs before
	// ChatGPT's JS can capture the original clipboard.writeText reference
	if err := installClipboardInterceptor(taskCtx); err != nil {
		log.Fatal("Failed to install clipboard interceptor:", err)
	}

	err = chromedp.Run(taskCtx,
		chromedp.Navigate(`https://chatgpt.com`),
		chromedp.WaitVisible(`#prompt-textarea`, chromedp.ByID),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := grantClipboardPermission(taskCtx); err != nil {
		log.Println("Warning: could not grant clipboard permission:", err)
	}

	// Register the custom client that routes prompts through CDP
	clients.RegisterCustomClient(&ChatbangClient{cdpCtx: taskCtx})

	// Initialize nekot TUI
	flags := config.StartupFlags{
		Model:           "chatgpt",
		StartNewSession: true,
	}
	nekotConfig := config.CreateAndValidateConfig(flags)

	appPath, _ := util.GetAppDataPath()
	f, err := tea.LogToFile(filepath.Join(appPath, "debug.log"), "debug")
	if err != nil {
		log.Fatal("fatal:", err)
	}
	defer f.Close()

	db := util.InitDb()
	err = util.MigrateFS(db, migrations.FS, ".")
	if err != nil {
		log.Fatal("Migration error:", err)
	}
	defer db.Close()

	ctx := context.Background()
	ctxWithConfig := config.WithConfig(ctx, &nekotConfig)
	appCtx := config.WithFlags(ctxWithConfig, &flags)
	zone.NewGlobal()

	p := tea.NewProgram(
		views.NewMainView(db, appCtx),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = p.Run()
	if err != nil {
		log.Fatal(err)
	}
}

/** grantClipboardPermission grants clipboard read/write via CDP so no browser prompt appears. */
func grantClipboardPermission(ctx context.Context) error {
	origin := "https://chatgpt.com"
	// SetPermission is a browser-level command — must use the browser executor, not the target executor
	c := chromedp.FromContext(ctx)
	browserCtx := cdp.WithExecutor(ctx, c.Browser)

	if err := browser.SetPermission(
		&browser.PermissionDescriptor{Name: "clipboard-read"},
		browser.PermissionSettingGranted,
	).WithOrigin(origin).Do(browserCtx); err != nil {
		return err
	}
	return browser.SetPermission(
		&browser.PermissionDescriptor{Name: "clipboard-write", AllowWithoutSanitization: true},
		browser.PermissionSettingGranted,
	).WithOrigin(origin).Do(browserCtx)
}

/** installClipboardInterceptor registers a script that runs before any page JS,
    overriding navigator.clipboard.writeText to capture copied text in a per-tab global. */
func installClipboardInterceptor(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(`
			window.__chatbangCaptured = null;
			if (navigator.clipboard && navigator.clipboard.writeText) {
				const orig = navigator.clipboard.writeText.bind(navigator.clipboard);
				navigator.clipboard.writeText = function(text) {
					window.__chatbangCaptured = text;
					return orig(text);
				};
			}
		`).Do(ctx)
		return err
	}))
}

/** sendAndWaitForResponse sends a prompt and waits for the response via the per-tab clipboard interceptor. */
func sendAndWaitForResponse(taskCtx context.Context, prompt string) (string, error) {
	buttonDiv := `button[data-testid="copy-turn-action-button"]`

	// Clear any previously captured text
	err := chromedp.Run(taskCtx,
		chromedp.Evaluate(`window.__chatbangCaptured = null`, nil),
	)
	if err != nil {
		return "", err
	}

	// Send the prompt
	err = chromedp.Run(taskCtx,
		chromedp.WaitVisible(`#prompt-textarea`, chromedp.ByID),
		chromedp.Click(`#prompt-textarea`, chromedp.ByID),
		chromedp.SendKeys(`#prompt-textarea`, prompt, chromedp.ByID),
		chromedp.Click(`#composer-submit-button`, chromedp.ByID),
	)
	if err != nil {
		return "", err
	}

	// Wait for copy button, click it, then read from the per-tab interceptor
	for {
		var copiedText string
		err = chromedp.Run(taskCtx,
			chromedp.Sleep(1*time.Second),
			chromedp.WaitVisible(buttonDiv, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(`
				(() => {
					let buttons = document.querySelectorAll('%s');
					if (buttons.length > 0) {
						buttons[buttons.length - 1].click();
					}
				})()
			`, buttonDiv), nil),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(`window.__chatbangCaptured || ""`, &copiedText),
		)
		if err != nil {
			return "", err
		}

		if len(copiedText) > 0 && copiedText != prompt {
			return copiedText, nil
		}
	}
}

func loginProfile(defaultBrowser string, profileDir string) {
	var taskCtx context.Context
	var taskCancel context.CancelFunc

	if goruntime.GOOS == "darwin" {
		ctx, cancel, err := launchChromeViaCDP(defaultBrowser, profileDir, chatbangDebugPort)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		taskCtx, taskCancel = context.WithTimeout(ctx, ctxTime*time.Second)
		defer taskCancel()
	} else {
		allocatorCtx, cancel := chromedp.NewExecAllocator(context.Background(),
			append(chromedp.DefaultExecAllocatorOptions[:],
				chromedp.ExecPath(defaultBrowser),
				chromedp.Flag("disable-blink-features", "AutomationControlled"),
				chromedp.Flag("exclude-switches", "enable-automation"),
				chromedp.Flag("disable-extensions", false),
				chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
				chromedp.Flag("disable-default-apps", false),
				chromedp.Flag("disable-dev-shm-usage", false),
				chromedp.Flag("disable-gpu", false),
				chromedp.Flag("headless", false),
				chromedp.UserDataDir(profileDir),
				chromedp.Flag("profile-directory", "Default"),
			)...,
		)
		defer cancel()

		ctx, ctxCancel := chromedp.NewContext(allocatorCtx)
		defer ctxCancel()

		taskCtx, taskCancel = context.WithTimeout(ctx, ctxTime*time.Second)
		defer taskCancel()
	}

	err := chromedp.Run(taskCtx,
		chromedp.Navigate(`https://www.chatgpt.com/`),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Log in to ChatGPT in the browser window.")
	fmt.Println("Once logged in, press Enter here to grant clipboard permission and finish setup.")

	bufio.NewReader(os.Stdin).ReadLine()

	if err := grantClipboardPermission(taskCtx); err != nil {
		fmt.Println("Warning: could not grant clipboard permission:", err)
	} else {
		fmt.Println("Clipboard permission granted.")
	}
	fmt.Println("Setup complete! Leave the browser window open and run 'chatbang' to start chatting.")
}
