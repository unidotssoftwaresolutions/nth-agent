package fakephone

import (
	"fmt"
	"sort"
	"strings"
)

// THE DEMO APP, AS A PHONE SHOWS IT. NthCase Demo (com.nthcase.demo) is a small made-up shop: sign in with the demo
// account, a grid of ten products with a search field, a product's details, a cart, an order. Its screens, ids, content
// descriptions and texts here are the app's own (its layouts and strings), and each screen is drawn the way UiAutomator2
// dumps it on a 1080×2340 Android phone: every node with the attributes a real dump carries, in the same order, bounds
// clipped to the screen, a hidden view absent.

// Package is the demo app's package.
const Package = "com.nthcase.demo"

// The phone's screen, in pixels: a 1080×2340 Android phone, its status bar and its navigation bar.
const (
	Width        = 1080
	Height       = 2340
	statusBarEnd = 77
	navBarStart  = 2205
)

const launcherPackage = "com.sec.android.app.launcher"

// The demo account the sign-in screen shows.
const (
	demoEmail    = "demo@nthcase.com"
	demoPassword = "demo1234"
)

type product struct {
	slug, name  string
	cents       int
	description string
}

var catalog = []product{
	{"mug", "Morning Mug", 1400, "A sturdy stoneware mug that holds a generous 350 ml. Glazed in warm terracotta and safe for the dishwasher."},
	{"notebook", "Grid Notebook", 950, "A5 notebook with 120 dotted-grid pages, a lay-flat binding and an elastic band to keep it closed."},
	{"plant", "Desk Plant", 1800, "An easy-going leafy plant in a clay pot. Happy on a desk, a shelf or a sunny window sill."},
	{"candle", "Cedar Candle", 1600, "A hand-poured candle in an amber jar with a soft cedar scent. Burns for about 40 hours."},
	{"tote", "Canvas Tote", 2200, "A roomy cotton canvas bag with long handles and a striped band. Folds flat when you don't need it."},
	{"bottle", "Steel Bottle", 2400, "A double-walled steel bottle that keeps drinks cold for a day and hot for half of one. Holds 500 ml."},
	{"clock", "Wall Clock", 3500, "A quiet wall clock with a clean white face and a silent movement that never ticks."},
	{"lamp", "Desk Lamp", 4200, "A small lamp with a fabric shade and a weighted base. Gives a warm light for reading."},
	{"sunglasses", "Sunglasses", 2800, "Classic sunglasses with dark lenses that block UV light. Comes with a soft case."},
	{"umbrella", "Rain Umbrella", 1900, "A bright umbrella with a curved handle that opens wide enough for two."},
}

func money(cents int) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

func productBySlug(slug string) (product, bool) {
	for _, p := range catalog {
		if p.slug == slug {
			return p, true
		}
	}
	return product{}, false
}

// The screens, by the activity that shows them.
const (
	screenLauncher    = "launcher"
	screenSignIn      = "SignInActivity"
	screenProducts    = "ProductsActivity"
	screenDetail      = "ProductDetailActivity"
	screenCart        = "CartActivity"
	screenOrderPlaced = "OrderPlacedActivity"
)

// app is the demo app on one phone: whether it is installed and in front, which screen it shows, and what the person has
// done on it. Nothing is saved: every launch starts signed out with an empty cart, as the app does.
type app struct {
	installed bool
	running   bool
	screen    string

	email, password string
	focus           string // the key of the field with focus
	signInError     bool
	search          string
	scroll          int // how far the product grid is scrolled, in pixels
	detail          string
	quantity        int
	cart            map[string]int
	snackbar        bool
}

func newApp() *app { return &app{installed: true, screen: screenLauncher} }

// launch starts the app fresh, on its sign-in screen.
func (a *app) launch() {
	*a = app{installed: a.installed, running: true, screen: screenSignIn, cart: map[string]int{}}
}

// activate brings the app to the front, starting it when it is not running.
func (a *app) activate() {
	if !a.running {
		a.launch()
		return
	}
	if a.screen == screenLauncher {
		a.screen = screenSignIn
	}
}

func (a *app) terminate() {
	a.running = false
	a.screen = screenLauncher
}

func (a *app) foreground() bool { return a.running && a.screen != screenLauncher }

// activity is what UiAutomator2 answers for the current activity.
func (a *app) activity() string {
	if !a.foreground() {
		return ".Launcher"
	}
	return "." + a.screen
}

func (a *app) packageName() string {
	if !a.foreground() {
		return launcherPackage
	}
	return Package
}

// ── what a tap does ────────────────────────────────────────────────────────────────────────────────────────────────

// tap acts on the node a tap landed on (its key), as the app's own listeners do.
func (a *app) tap(key string) {
	if a.screen != screenDetail {
		a.snackbar = false
	}
	switch a.screen {
	case screenLauncher:
		if key == "launcher_app_icon" {
			a.launch()
		}
	case screenSignIn:
		switch key {
		case "email_input", "password_input":
			a.focus = key
		case "use_demo_account":
			a.email, a.password = demoEmail, demoPassword
		case "sign_in_button":
			if a.email == demoEmail && a.password == demoPassword {
				a.signInError = false
				a.screen = screenProducts
				a.focus, a.search, a.scroll = "", "", 0
			} else {
				a.signInError = true
			}
		}
	case screenProducts:
		switch {
		case key == "search_input":
			a.focus = key
		case key == "cart_action":
			a.screen = screenCart
		case key == "action_sign_out":
			a.launch()
		case strings.HasPrefix(key, "product_"):
			slug := strings.TrimPrefix(strings.SplitN(key, "/", 2)[0], "product_")
			if _, ok := productBySlug(slug); ok {
				a.screen, a.detail, a.quantity, a.snackbar, a.focus = screenDetail, slug, 1, false, ""
			}
		}
	case screenDetail:
		a.snackbar = false
		switch key {
		case "navigate_up":
			a.screen = screenProducts
		case "qty_decrease":
			if a.quantity > 1 {
				a.quantity--
			}
		case "qty_increase":
			if a.quantity < 10 {
				a.quantity++
			}
		case "add_to_cart":
			a.cart[a.detail] += a.quantity
			if a.cart[a.detail] > 10 {
				a.cart[a.detail] = 10
			}
			a.snackbar = true
		case "snackbar_action":
			a.screen = screenCart
		}
	case screenCart:
		switch {
		case key == "navigate_up", key == "continue_shopping", key == "browse_products":
			a.screen = screenProducts
		case key == "checkout_button":
			if len(a.cart) > 0 {
				a.cart = map[string]int{}
				a.screen = screenOrderPlaced
			}
		case strings.HasPrefix(key, "cart_line_"):
			parts := strings.SplitN(key, "/", 2)
			slug := strings.TrimPrefix(parts[0], "cart_line_")
			if len(parts) == 2 {
				switch parts[1] {
				case "cart_line_increase":
					if a.cart[slug] < 10 {
						a.cart[slug]++
					}
				case "cart_line_decrease":
					if a.cart[slug] > 1 {
						a.cart[slug]--
					}
				case "cart_line_remove":
					delete(a.cart, slug)
				}
			}
		}
	case screenOrderPlaced:
		if key == "back_to_products" || key == "navigate_up" {
			a.screen = screenProducts
		}
	}
}

// typeInto sets a field's text as UiAutomator2's set-text does, and runs what the app runs on a change.
func (a *app) typeInto(key, text string, replace bool) bool {
	set := func(field *string) {
		if replace {
			*field = text
		} else {
			*field += text
		}
	}
	switch {
	case a.screen == screenSignIn && key == "email_input":
		set(&a.email)
	case a.screen == screenSignIn && key == "password_input":
		set(&a.password)
	case a.screen == screenProducts && key == "search_input":
		set(&a.search)
		a.scroll = 0
	default:
		return false
	}
	a.focus = key
	return true
}

func (a *app) clear(key string) bool { return a.typeInto(key, "", true) }

// back is the phone's back button.
func (a *app) back() {
	a.snackbar = false
	switch a.screen {
	case screenDetail, screenCart, screenOrderPlaced:
		a.screen = screenProducts
	case screenSignIn, screenProducts:
		a.screen = screenLauncher // the activity finishes; the process stays
	}
}

// scrollBy moves the product grid; it answers whether it can scroll further that way.
func (a *app) scrollBy(dy int) bool {
	if a.screen != screenProducts {
		return false
	}
	max := a.maxScroll()
	a.scroll += dy
	if a.scroll < 0 {
		a.scroll = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
	if dy > 0 {
		return a.scroll < max
	}
	return a.scroll > 0
}

func (a *app) shown() []product {
	var out []product
	needle := strings.ToLower(strings.TrimSpace(a.search))
	for _, p := range catalog {
		if needle == "" || strings.Contains(strings.ToLower(p.name), needle) {
			out = append(out, p)
		}
	}
	return out
}

// ── how each screen is drawn ───────────────────────────────────────────────────────────────────────────────────────

const (
	gridTop    = 441
	cardW      = 486
	cardH      = 690
	cardGap    = 24
	gridMargin = 42
)

func (a *app) maxScroll() int {
	rows := (len(a.shown()) + 1) / 2
	content := gridMargin + rows*(cardH+cardGap)
	if over := content - (navBarStart - gridTop); over > 0 {
		return over
	}
	return 0
}

// render draws the current screen as the node tree a page source is written from.
func (a *app) render() *node {
	if !a.foreground() {
		return launcherTree()
	}
	var content []*node
	switch a.screen {
	case screenSignIn:
		content = a.signInContent()
	case screenProducts:
		content = a.productsContent()
	case screenDetail:
		content = a.detailContent()
	case screenCart:
		content = a.cartContent()
	case screenOrderPlaced:
		content = a.orderPlacedContent()
	}
	return appWindow(content)
}

func id(name string) string { return Package + ":id/" + name }

// appWindow is what every activity of the app sits in: the decor view, the action bar root and android:id/content.
func appWindow(content []*node) *node {
	contentFrame := view("android.widget.FrameLayout", 0, statusBarEnd, Width, navBarStart, content...)
	contentFrame.rid = "android:id/content"
	actionBarRoot := view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart, contentFrame)
	actionBarRoot.rid = id("action_bar_root")
	decor := view("android.widget.LinearLayout", 0, 0, Width, navBarStart,
		view("android.widget.FrameLayout", 0, statusBarEnd, Width, navBarStart, actionBarRoot))
	root := view("android.widget.FrameLayout", 0, 0, Width, Height, decor)
	setPackage(root, Package)
	return root
}

func launcherTree() *node {
	icon := view("android.widget.TextView", 60, 1700, 300, 1960)
	icon.text, icon.desc, icon.key, icon.clickable, icon.focusable = "NthCase Demo", "NthCase Demo", "launcher_app_icon", true, true
	workspace := view("android.view.ViewGroup", 0, statusBarEnd, Width, navBarStart, icon)
	workspace.rid = launcherPackage + ":id/workspace"
	root := view("android.widget.FrameLayout", 0, 0, Width, Height,
		view("android.widget.FrameLayout", 0, 0, Width, Height, workspace))
	setPackage(root, launcherPackage)
	return root
}

func toolbar(title string, back bool) *node {
	var children []*node
	if back {
		up := view("android.widget.ImageButton", 0, statusBarEnd, 147, 224)
		up.desc, up.key, up.clickable, up.focusable = "Back", "navigate_up", true, true
		children = append(children, up)
	}
	left := 42
	if back {
		left = 189
	}
	t := view("android.widget.TextView", left, 115, 760, 186)
	t.text = title
	children = append(children, t)
	bar := view("android.view.ViewGroup", 0, statusBarEnd, Width, 224, children...)
	bar.rid, bar.key = id("toolbar"), "toolbar"
	return view("android.widget.LinearLayout", 0, statusBarEnd, Width, 224, bar)
}

func editText(key, hint, value string, password bool, focus string, l, t, r, b int) *node {
	field := view("android.widget.EditText", l, t+21, r, b-7)
	field.rid, field.key, field.desc = id(key), key, hint
	field.clickable, field.focusable, field.longClickable, field.password = true, true, true, password
	field.focused = focus == key
	switch {
	case value == "":
		field.text, field.showingHint = hint, true
	case password:
		field.text = strings.Repeat("•", len([]rune(value)))
	default:
		field.text = value
	}
	frame := view("android.widget.FrameLayout", l, t+21, r, b-7, field)
	layout := view("android.widget.LinearLayout", l, t, r, b, frame)
	layout.rid = id(strings.TrimSuffix(key, "_input") + "_layout")
	return layout
}

func button(key, text string, l, t, r, b int) *node {
	n := view("android.widget.Button", l, t, r, b)
	n.rid, n.key, n.text, n.desc, n.clickable, n.focusable = id(key), key, text, text, true, true
	return n
}

func textView(key, text string, l, t, r, b int) *node {
	n := view("android.widget.TextView", l, t, r, b)
	n.text, n.key = text, key
	if key != "" {
		n.rid = id(key)
	}
	return n
}

func (a *app) signInContent() []*node {
	brand := view("android.widget.ImageView", 456, 140, 624, 308)
	brand.rid, brand.desc, brand.key = id("brand_mark"), "NthCase Demo logo", "brand_mark"
	form := []*node{
		textView("", "Sign in", 42, 560, 1038, 640),
		editText("email_input", "E-mail", a.email, false, a.focus, 42, 661, 1038, 850),
		editText("password_input", "Password", a.password, true, a.focus, 42, 871, 1038, 1060),
	}
	buttonTop := 1102
	if a.signInError {
		e := textView("sign_in_error", "Wrong e-mail or password.", 42, 1081, 1038, 1137)
		e.desc = "Wrong e-mail or password."
		form = append(form, e)
		buttonTop = 1158
	}
	form = append(form, button("sign_in_button", "Sign in", 42, buttonTop, 1038, buttonTop+147))
	cardTop := buttonTop + 189
	details := textView("demo_account_details", "E-mail: "+demoEmail+"\nPassword: "+demoPassword, 84, cardTop+105, 996, cardTop+217)
	details.desc = "Demo account. E-mail: " + demoEmail + ". Password: " + demoPassword + "."
	card := view("android.widget.FrameLayout", 42, cardTop, 1038, cardTop+420,
		view("android.widget.LinearLayout", 42, cardTop, 1038, cardTop+420,
			textView("", "Demo account", 84, cardTop+42, 996, cardTop+98),
			details,
			button("use_demo_account", "Use demo account", 84, cardTop+238, 996, cardTop+378)))
	card.rid = id("demo_account")
	form = append(form, card)
	column := view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart,
		brand,
		textView("app_title", "NthCase Demo", 42, 329, 1038, 413),
		textView("store_tagline", "A small shop to try NthCase on", 42, 434, 1038, 490),
		view("android.widget.LinearLayout", 0, 532, Width, cardTop+420, form...))
	scroll := view("android.widget.ScrollView", 0, statusBarEnd, Width, navBarStart, column)
	scroll.scrollable, scroll.focusable = true, true
	return []*node{scroll}
}

func (a *app) cartCount() int {
	n := 0
	for _, q := range a.cart {
		n += q
	}
	return n
}

func (a *app) productsContent() []*node {
	bar := toolbar("Fernwood Goods", false)
	cart := view("android.widget.FrameLayout", 840, 98, 945, 203,
		view("android.widget.ImageView", 861, 119, 924, 182))
	cart.rid, cart.desc, cart.key, cart.clickable, cart.focusable = id("cart_action"), "Cart", "cart_action", true, true
	if count := a.cartCount(); count > 0 {
		cart.children = append(cart.children, textView("cart_count", fmt.Sprint(count), 903, 105, 945, 147))
	}
	signOut := view("android.widget.Button", 945, 98, 1059, 203)
	signOut.rid, signOut.desc, signOut.key, signOut.clickable, signOut.focusable = id("action_sign_out"), "Sign out", "action_sign_out", true, true
	bar.children[0].children = append(bar.children[0].children, cart, signOut)

	search := editText("search_input", "Search products", a.search, false, a.focus, 42, 245, 1038, 420)
	area := view("android.widget.FrameLayout", 0, gridTop, Width, navBarStart)
	shown := a.shown()
	if len(shown) == 0 {
		empty := textView("products_empty", "No products match your search.", 42, 1250, 1038, 1306)
		empty.desc = "No products match your search."
		area.children = append(area.children, empty)
	} else {
		list := view("androidx.recyclerview.widget.RecyclerView", 0, gridTop, Width, navBarStart)
		list.rid, list.desc, list.key, list.scrollable, list.focusable = id("product_list"), "Fernwood Goods", "product_list", true, true
		for i, p := range shown {
			col, row := i%2, i/2
			l := gridMargin + col*(cardW+cardGap+12)
			t := gridTop + gridMargin + row*(cardH+cardGap) - a.scroll
			if t+cardH <= gridTop || t >= navBarStart {
				continue // RecyclerView lays out only the cards on screen
			}
			key := "product_" + p.slug
			image := view("android.widget.ImageView", l, t, l+cardW, t+cardW)
			image.rid, image.key = id("product_image"), key+"/product_image"
			name := textView("product_name", p.name, l+32, t+cardW+21, l+cardW-32, t+cardW+91)
			name.key = key + "/product_name"
			price := textView("product_price", money(p.cents), l+32, t+cardW+105, l+cardW-32, t+cardW+168)
			price.key = key + "/product_price"
			card := view("android.widget.FrameLayout", l, t, l+cardW, t+cardH,
				view("android.widget.LinearLayout", l, t, l+cardW, t+cardH, image, name, price))
			card.rid, card.key, card.desc = id(key), key, p.name+", "+money(p.cents)
			card.clickable, card.focusable = true, true
			list.children = append(list.children, card)
		}
		area.children = append(area.children, list)
	}
	return []*node{view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart, bar, search, area)}
}

func (a *app) detailContent() []*node {
	p, _ := productBySlug(a.detail)
	image := view("android.widget.ImageView", 42, 245, 1038, 1241)
	image.rid, image.key, image.desc = id("detail_image"), "detail_image", "Picture of "+p.name
	quantity := textView("qty_value", fmt.Sprint(a.quantity), 480, 1704, 600, 1781)
	quantity.desc = fmt.Sprintf("Quantity %d", a.quantity)
	decrease := button("qty_decrease", "", 300, 1680, 447, 1806)
	decrease.desc = "Decrease quantity"
	increase := button("qty_increase", "", 633, 1680, 780, 1806)
	increase.desc = "Increase quantity"
	column := view("android.widget.LinearLayout", 0, 224, Width, 2016,
		image,
		textView("detail_name", p.name, 42, 1283, 1038, 1360),
		textView("detail_price", money(p.cents), 42, 1381, 1038, 1444),
		textView("detail_description", p.description, 42, 1486, 1038, 1612),
		textView("", "Quantity", 42, 1633, 1038, 1675),
		view("android.widget.LinearLayout", 300, 1680, 780, 1806, decrease, quantity, increase))
	scroll := view("android.widget.ScrollView", 0, 224, Width, 2016, column)
	scroll.scrollable, scroll.focusable = true, true
	nodes := []*node{toolbar("Product details", true), scroll, button("add_to_cart", "Add to cart", 42, 2037, 1038, 2184)}
	root := view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart, nodes...)
	root.rid = id("detail_root")
	out := []*node{root}
	if a.snackbar {
		text := textView("snackbar_text", "Added to cart", 84, 1940, 760, 2010)
		action := button("snackbar_action", "View cart", 780, 1919, 1017, 2031)
		bar := view("android.widget.FrameLayout", 42, 1898, 1038, 2052, view("android.widget.LinearLayout", 42, 1898, 1038, 2052, text, action))
		out = append(out, bar)
	}
	return out
}

func (a *app) cartContent() []*node {
	nodes := []*node{toolbar("Cart", true)}
	if len(a.cart) == 0 {
		empty := view("android.widget.LinearLayout", 0, 224, Width, navBarStart,
			textView("cart_empty_title", "Your cart is empty", 42, 1100, 1038, 1180),
			textView("", "Pick something you like from the products.", 42, 1201, 1038, 1257),
			button("browse_products", "Browse products", 240, 1320, 840, 1467))
		empty.rid, empty.key, empty.desc = id("cart_empty"), "cart_empty", "Your cart is empty"
		return append(nodes, empty)
	}
	var slugs []string
	for slug := range a.cart {
		slugs = append(slugs, slug)
	}
	sort.Slice(slugs, func(i, j int) bool { return catalogIndex(slugs[i]) < catalogIndex(slugs[j]) })
	total := 0
	list := view("androidx.recyclerview.widget.RecyclerView", 0, 224, Width, 1700)
	list.scrollable = true
	for i, slug := range slugs {
		p, _ := productBySlug(slug)
		q := a.cart[slug]
		total += q * p.cents
		t := 245 + i*252
		key := "cart_line_" + slug
		sub := func(name, text, desc string, l, r int, clickable bool) *node {
			n := textView(name, text, l, t+105, r, t+210)
			n.key, n.desc = key+"/"+name, desc
			if clickable {
				n.class, n.clickable, n.focusable = "android.widget.Button", true, true
			}
			return n
		}
		line := view("android.widget.LinearLayout", 0, t, Width, t+231,
			textView("", p.name, 42, t+21, 700, t+84),
			sub("cart_line_decrease", "", "Decrease "+p.name, 42, 168, true),
			sub("cart_line_qty", fmt.Sprint(q), fmt.Sprintf("Quantity of %s: %d", p.name, q), 189, 290, false),
			sub("cart_line_increase", "", "Increase "+p.name, 311, 437, true),
			sub("cart_line_remove", "Remove", "Remove "+p.name, 480, 700, true),
			sub("cart_line_total", money(q*p.cents), p.name+" total "+money(q*p.cents), 760, 1038, false))
		line.rid, line.key, line.desc = id(key), key, p.name+" in cart"
		list.children = append(list.children, line)
	}
	totalText := textView("cart_total", money(total), 600, 1764, 1038, 1834)
	totalText.desc = "Total " + money(total)
	nodes = append(nodes, list, textView("", "Total", 42, 1764, 500, 1834), totalText,
		button("checkout_button", "Checkout", 42, 1876, 1038, 2023),
		button("continue_shopping", "Continue shopping", 42, 2044, 1038, 2184))
	return []*node{view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart, nodes...)}
}

func catalogIndex(slug string) int {
	for i, p := range catalog {
		if p.slug == slug {
			return i
		}
	}
	return len(catalog)
}

func (a *app) orderPlacedContent() []*node {
	picture := view("android.widget.ImageView", 340, 700, 740, 1100)
	picture.desc = "A check mark"
	return []*node{view("android.widget.LinearLayout", 0, statusBarEnd, Width, navBarStart,
		toolbar("Order placed", false), picture,
		textView("order_placed_title", "Order placed", 42, 1150, 1038, 1240),
		textView("order_placed_message", "Thank you! Your order is confirmed and on its way.", 42, 1261, 1038, 1380),
		button("back_to_products", "Back to products", 240, 1450, 840, 1597))}
}
