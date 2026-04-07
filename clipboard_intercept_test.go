package main

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

/** polyfillClipboard injects a fake navigator.clipboard for data: URLs where the API doesn't exist. */
func polyfillClipboard(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.Evaluate(`
		if (!navigator.clipboard) {
			navigator.clipboard = {
				_text: "",
				writeText: function(text) { this._text = text; return Promise.resolve(); },
				readText: function() { return Promise.resolve(this._text); },
			};
		}
	`, nil))
}

/** TestClipboardInterceptor verifies the per-tab clipboard interceptor captures text
    without relying on the system clipboard. */
func TestClipboardInterceptor(t *testing.T) {
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Navigate to a minimal page
	err := chromedp.Run(ctx,
		chromedp.Navigate(`data:text/html,<html><body><button id="btn">Copy</button></body></html>`),
		chromedp.WaitVisible(`#btn`, chromedp.ByID),
	)
	if err != nil {
		t.Fatal("navigate:", err)
	}

	// Polyfill clipboard API (not available on data: URLs)
	if err := polyfillClipboard(ctx); err != nil {
		t.Fatal("polyfill:", err)
	}

	// Install interceptor
	err = installClipboardInterceptor(ctx)
	if err != nil {
		t.Fatal("install interceptor:", err)
	}

	// Verify initial state is null
	var initial string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`window.__chatbangCaptured || ""`, &initial),
	)
	if err != nil {
		t.Fatal("read initial:", err)
	}
	if initial != "" {
		t.Fatalf("expected empty initial capture, got %q", initial)
	}

	// Simulate a clipboard write (like ChatGPT's copy button would do)
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`navigator.clipboard.writeText("hello from tab")`, nil),
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal("simulate write:", err)
	}

	// Read captured text
	var captured string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`window.__chatbangCaptured || ""`, &captured),
	)
	if err != nil {
		t.Fatal("read captured:", err)
	}
	if captured != "hello from tab" {
		t.Fatalf("expected %q, got %q", "hello from tab", captured)
	}

	// Verify clearing works
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`window.__chatbangCaptured = null`, nil),
	)
	if err != nil {
		t.Fatal("clear:", err)
	}
	var afterClear string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`window.__chatbangCaptured || ""`, &afterClear),
	)
	if err != nil {
		t.Fatal("read after clear:", err)
	}
	if afterClear != "" {
		t.Fatalf("expected empty after clear, got %q", afterClear)
	}
}

/** TestTwoTabsIsolated verifies two tabs have independent interceptors. */
func TestTwoTabsIsolated(t *testing.T) {
	allocCtx, allocCancel := chromedp.NewContext(context.Background())
	defer allocCancel()

	// Tab 1
	ctx1, cancel1 := chromedp.NewContext(allocCtx)
	defer cancel1()
	ctx1, cancel1timeout := context.WithTimeout(ctx1, 15*time.Second)
	defer cancel1timeout()

	// Tab 2
	ctx2, cancel2 := chromedp.NewContext(allocCtx)
	defer cancel2()
	ctx2, cancel2timeout := context.WithTimeout(ctx2, 15*time.Second)
	defer cancel2timeout()

	page := `data:text/html,<html><body>tab</body></html>`

	// Navigate both tabs
	err := chromedp.Run(ctx1, chromedp.Navigate(page))
	if err != nil {
		t.Fatal("navigate tab1:", err)
	}
	err = chromedp.Run(ctx2, chromedp.Navigate(page))
	if err != nil {
		t.Fatal("navigate tab2:", err)
	}

	// Polyfill + install interceptors on both
	if err := polyfillClipboard(ctx1); err != nil {
		t.Fatal("polyfill tab1:", err)
	}
	if err := polyfillClipboard(ctx2); err != nil {
		t.Fatal("polyfill tab2:", err)
	}
	if err := installClipboardInterceptor(ctx1); err != nil {
		t.Fatal("install tab1:", err)
	}
	if err := installClipboardInterceptor(ctx2); err != nil {
		t.Fatal("install tab2:", err)
	}

	// Write different text in each tab
	err = chromedp.Run(ctx1,
		chromedp.Evaluate(`navigator.clipboard.writeText("tab1 response")`, nil),
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal("write tab1:", err)
	}
	err = chromedp.Run(ctx2,
		chromedp.Evaluate(`navigator.clipboard.writeText("tab2 response")`, nil),
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal("write tab2:", err)
	}

	// Verify each tab captured its own text
	var cap1, cap2 string
	err = chromedp.Run(ctx1, chromedp.Evaluate(`window.__chatbangCaptured || ""`, &cap1))
	if err != nil {
		t.Fatal("read tab1:", err)
	}
	err = chromedp.Run(ctx2, chromedp.Evaluate(`window.__chatbangCaptured || ""`, &cap2))
	if err != nil {
		t.Fatal("read tab2:", err)
	}

	if cap1 != "tab1 response" {
		t.Fatalf("tab1: expected %q, got %q", "tab1 response", cap1)
	}
	if cap2 != "tab2 response" {
		t.Fatalf("tab2: expected %q, got %q", "tab2 response", cap2)
	}
}
