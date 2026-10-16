# Personalising the interface

Everything here only changes how the interface looks for you. None of it
grants or removes access.

## Where preferences are kept

Preferences are saved to your account (`ui_prefs` on `PATCH /api/v1/me`,
returned by `GET /api/v1/me`), so they follow you to other browsers. They are
also cached in the browser, so pages draw with them straight away; changes are
saved to the cache at once and sent to the server shortly after, in one batch.
Interface preferences are not written to the audit log. Each user's are kept
apart, also in a shared browser.

Saved per user: theme, sidebar order, sidebar layout (Add Features), table
columns, sort and grouping, and collapsed groups on the Cloudflare, Caddy and
Nginx pages.

## Account, light and dark mode, themes

The account button sits at the bottom of the sidebar. Its popup has Light
(sun), Dark (moon) and Match system, Theme, your account and Authenticator
pages, and Log out.

Theme opens a picker with several themes, each shown as a small preview. The
accent colour can be the theme's own, a preset, any colour from the colour
picker, or Rainbow, which turns slowly through every colour (and stays still
when the system asks for reduced motion). Changes show across the app while
the picker is open; Apply saves them and Cancel puts the saved theme back.
Brightness is kept per mode, so text stays readable whatever colour is picked.

## Sidebar order

Drag an entry within its category to move it. Right-click an entry for Move
up, Move down and Reset; with the keyboard, focus an entry and press Alt+Up or
Alt+Down. Entries keep their category.

## Right-click

The browser's own right-click menu does not open on the site. Rows, cards,
tunnels and sidebar entries open their own menus. Anywhere else a site menu
opens with what fits: Cut, Copy, Paste and Select all in text fields; Copy,
Paste, Select all and Clear in terminals; Open, Open in new tab and Copy link
on links; the page's own actions (for example Add hostname on Cloudflare);
and Back, Forward, Reload, Search and Copy page link. The code editor and the
VM console keep their own menus. Shift+F10 or the menu key opens it from the
keyboard.
