// this file contains my firefox preferences.
// there are more complicated ones:
// - https://github.com/pyllyukko/user.js
// - https://github.com/arkenfox/user.js
// - https://gist.github.com/gagarine/5cf8f861abe0dd035b7af19e4f691cd8
// but i don't care about that level of detail.
user_pref("browser.aboutConfig.showWarning", false);
user_pref("browser.cache.disk.enable", false);
user_pref("browser.contentblocking.category", "strict");
user_pref("browser.download.panel.shown", true);
user_pref("browser.download.useDownloadDir", false);
user_pref("browser.formfill.enable", false);
user_pref("browser.newtabpage.enabled", false);
user_pref("browser.rights.3.shown", true);
user_pref("browser.search.suggest.enabled", false);
user_pref("browser.sessionstore.interval", 6000000);
user_pref("browser.startup.homepage", "about:blank");
user_pref("browser.tabs.closeWindowWithLastTab", false);
user_pref("browser.urlbar.shortcuts.bookmarks", false);
user_pref("browser.urlbar.shortcuts.history", false);
user_pref("browser.urlbar.shortcuts.tabs", false);
user_pref("browser.urlbar.suggest.bookmark", false);
user_pref("browser.urlbar.suggest.history", false);
user_pref("browser.urlbar.suggest.openpage", false);
user_pref("browser.urlbar.suggest.topsites", false);
user_pref("datareporting.healthreport.uploadEnabled", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("devtools.chrome.enabled", true);
user_pref("devtools.everOpened", true);
user_pref("font.default.x-western", "sans-serif");
user_pref("general.smoothScroll", false);
user_pref("keyword.enabled", false);
user_pref("network.cookie.cookieBehavior", 5);
user_pref("privacy.trackingprotection.enabled", true);
user_pref("privacy.trackingprotection.socialtracking.enabled", true);
user_pref("signon.rememberSignons", false);
user_pref("ui.caretBlinkTime", 0);
user_pref("ui.prefersReducedMotion", 1);
user_pref("widget.gtk.overlay-scrollbars.enabled", true);

// font overrides are disabled since i have a different config on ipi.
// user_pref("font.name.monospace.x-western", "Terminus");
// user_pref("font.name.sans-serif.x-western", "Noto Sans");
// user_pref("font.name.serif.x-western", "Noto Serif");
