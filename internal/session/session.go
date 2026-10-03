// Package session tells whether the agent is running inside a desktop session
// (someone logged in at a screen) or headless (a service on a booth box). The
// agent uses it to decide whether to show a tray icon and whether mpv should
// open a desktop window or draw straight to the screen through DRM/KMS.
package session
