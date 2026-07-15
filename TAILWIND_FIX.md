# Tailwind v4 + Templ Parsing Fix

## The Problem
Currently, adding a new responsive class (e.g., `sm:flex-col`, `dark:bg-black`) to a `.templ` file during local development (`make dev`) does not apply the styling. You are forced to kill the dev server and restart it for the new responsive classes to be recognized. 

## The Root Cause
Tailwind v4's Oxide parser sometimes struggles to extract classes from non-standard HTML attributes, specifically inside `templ` conditional blocks like `class={ templ.KV(...) }`. 

To fix this, you built a custom Go AST regex parser (`tools/generate_css/main.go`) that extracts these classes and injects them into `app/_entry.css` using the `@source inline(...)` directive.

However, in your `Makefile`, the `generate-css` target only runs **once**, right before the `dev` watch loop starts:
```make
dev: $(TAILWIND_BIN) bundle
	@$(MAKE) generate-css
	@$(MAKE) css
	@bash -c 'trap "kill 0" EXIT; $(TAILWIND_BIN) -i app/_entry.css -o app/assets/globals.css.output --watch & air; wait'
```
Because the custom Go script is not hooked into `air`'s hot-reload sequence, any responsive class you type during a dev session is never extracted, and the Tailwind watcher ignores it.

## The Elegant (Native) Solution
You don't need the custom Go AST parser. 

While Tailwind v4 struggles to parse `.templ` files, it parses `.go` files perfectly. When `air` detects a change and runs `templ generate`, all of your templates are transpiled into `*_templ.go` files. Inside these files, even complex conditional classes are compiled down into standard Go string constants (e.g., `"dark:bg-black"`). 

The Tailwind Oxide parser is incredibly fast and flawlessly extracts classes from Go string literals. By telling Tailwind to watch the generated `.go` files instead of the raw `.templ` files, you get perfect, native hot-reloading without any duct tape.

## Action Items

**1. Update `app/_entry.css`**
Delete the auto-generated `@source inline(...)` directive at the top of the file, and replace it with a native source directive pointing to the transpiled Go files:
```css
@import "tailwindcss";
@source "./**/*_templ.go";

@custom-variant dark (&:where(.dark, .dark *));
/* ... rest of the file ... */
```

**2. Clean up the `Makefile`**
Remove all references to the custom parser.
```diff
- ## css: Compile Tailwind CSS (scans root source templ files)
- css: generate-css
+ css:
	@which $(TAILWIND_BIN) > /dev/null && $(TAILWIND_BIN) -i app/_entry.css -o app/assets/globals.css.output --minify || npx @tailwindcss/cli -i app/_entry.css -o app/assets/globals.css.output --minify

- ## generate-css: Extract responsive classes from .templ files
- generate-css:
- 	@go run tools/generate_css/main.go

## dev: Run live-reloading dev server
dev: $(TAILWIND_BIN) bundle
- 	@$(MAKE) generate-css
	@$(MAKE) css
	@bash -c 'trap "kill 0" EXIT; $(TAILWIND_BIN) -i app/_entry.css -o app/assets/globals.css.output --watch & air; wait'
```

**3. Delete the custom tool**
You can safely delete the entire `tools/generate_css` directory.
```bash
rm -rf tools/generate_css
```

Once this is done, `air` will regenerate the `_templ.go` files on save, and the Tailwind watcher will instantly see the new classes in the Go strings and update the CSS. Zero downtime, zero missed classes.
