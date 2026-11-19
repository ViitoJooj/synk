//! synk's window. It holds no logic of its own: every screen is the agent's
//! control API, passed through as JSON, and the truth stays in the agent.

mod control;

use serde_json::Value;
use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Emitter, Manager, WindowEvent,
};

/// Passes one request to the agent.
#[tauri::command]
async fn control(verb: String, args: Option<Value>) -> Result<Value, control::ControlError> {
    tauri::async_runtime::spawn_blocking(move || control::call(&verb, args.unwrap_or(Value::Null)))
        .await
        .map_err(|e| control::ControlError {
            code: "snk-99".into(),
            message: e.to_string(),
            kind: "internal error".into(),
            hint: None,
            cause: None,
        })?
}

/// Brings up what the link needs, the way 'synk up' does. Nothing starts at
/// boot, so opening the window is what starts synk.
///
/// On Linux that is the injector (a system unit, started without a password by
/// the polkit rule the installer puts in place) and then the agent. A failed
/// injector start is not fatal here: it may already be running, and if it is
/// really down the agent says so by itself.
fn start_services() -> Result<(), String> {
    #[cfg(unix)]
    let out = {
        let _ = std::process::Command::new("systemctl").args(["start", "synk-injector.service"]).output();
        std::process::Command::new("systemctl").args(["--user", "start", "synk-agent.service"]).output()
    };
    #[cfg(windows)]
    let out = {
        use std::os::windows::process::CommandExt;
        // CREATE_NO_WINDOW: no console flashing up behind the window.
        std::process::Command::new("powershell.exe")
            .args(["-NoProfile", "-NonInteractive", "-Command", "Start-ScheduledTask -TaskName synk"])
            .creation_flags(0x0800_0000)
            .output()
    };
    match out {
        Ok(o) if o.status.success() => Ok(()),
        Ok(o) => Err(String::from_utf8_lossy(&o.stderr).trim().to_owned()),
        Err(e) => Err(e.to_string()),
    }
}

/// Starts the agent through the service manager, the way 'synk up' does.
#[tauri::command]
async fn start_agent() -> Result<(), control::ControlError> {
    tauri::async_runtime::spawn_blocking(start_services)
        .await
        .map_err(|e| e.to_string())
        .and_then(|r| r)
        .map_err(|msg| control::ControlError {
            code: "snk-05".into(),
            message: "could not start the agent".into(),
            kind: "environment error".into(),
            hint: Some("install it first: hacks/install.sh on Linux; on Windows the installer does this, so reinstall synk or run infra/windows/install.ps1".into()),
            cause: Some(msg),
        })
}

fn show(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

/// tray_text is the tray's tooltip: one line on the link.
fn tray_text(status: &Option<Value>) -> String {
    let Some(st) = status else { return "synk: agent not running".into() };
    let link = &st["link"];
    match link["state"].as_str() {
        Some("connected") => format!("synk: linked with {}", link["peer"].as_str().unwrap_or("the peer")),
        Some("waiting") => format!("synk: waiting for {}", link["peer"].as_str().unwrap_or("the peer")),
        _ => "synk: starting".into(),
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .invoke_handler(tauri::generate_handler![control, start_agent])
        .setup(|app| {
            let open = MenuItem::with_id(app, "open", "Open synk", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "Quit the window (the agent keeps running)", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&open, &quit])?;
            let tray = TrayIconBuilder::with_id("synk")
                .icon(app.default_window_icon().cloned().expect("the bundle has an icon"))
                .tooltip("synk")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "open" => show(app),
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = event {
                        show(tray.app_handle());
                    }
                })
                .build(app)?;

            // Opening synk is what starts it: nothing runs at boot. Failing
            // here is silent on purpose; the window shows "agent down" with a
            // button that retries and reports why.
            std::thread::spawn(|| {
                let _ = start_services();
            });

            // One thread follows the agent and fans its status out to the
            // window and the tray.
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                control::watch(|status| {
                    let _ = tray.set_tooltip(Some(tray_text(&status)));
                    let _ = handle.emit("synk://status", status);
                });
            });
            Ok(())
        })
        // Closing the window hides it to the tray: the window is a view on the
        // agent, and reopening it should be instant.
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running synk");
}
