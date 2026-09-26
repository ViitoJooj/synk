//! The agent's control API: JSON, one message per line, over a unix socket on
//! Linux and a named pipe on Windows. The same protocol the command line
//! speaks (internal/control); nothing is decided here, only passed along.

use serde::Serialize;
use serde_json::{json, Value};
use std::io::{BufRead, BufReader, Read, Write};

/// An errorx error from the agent, or "not-running" when nothing answers.
#[derive(Debug, Serialize, Clone)]
pub struct ControlError {
    pub code: String,
    pub message: String,
    pub kind: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hint: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cause: Option<String>,
}

impl ControlError {
    fn not_running(err: std::io::Error) -> Self {
        ControlError {
            code: "not-running".into(),
            message: "the agent is not running".into(),
            kind: "environment error".into(),
            hint: Some("start it here, or with 'synk up'".into()),
            cause: Some(err.to_string()),
        }
    }

    fn internal(msg: impl Into<String>) -> Self {
        ControlError {
            code: "snk-99".into(),
            message: msg.into(),
            kind: "internal error".into(),
            hint: None,
            cause: None,
        }
    }
}

#[cfg(unix)]
pub fn address() -> String {
    use std::os::unix::fs::MetadataExt;
    let dir = std::env::var("XDG_RUNTIME_DIR").unwrap_or_else(|_| {
        // /proc/self belongs to the user running this process: its uid,
        // without reaching for libc.
        let uid = std::fs::metadata("/proc/self").map(|m| m.uid()).unwrap_or(0);
        format!("/run/user/{uid}")
    });
    format!("{dir}/synk/control.sock")
}

#[cfg(windows)]
pub fn address() -> String {
    format!(r"\\.\pipe\synk-control-{}", std::env::var("USERNAME").unwrap_or_default())
}

type Conn = (Box<dyn Read + Send>, Box<dyn Write + Send>);

#[cfg(unix)]
fn connect() -> std::io::Result<Conn> {
    let stream = std::os::unix::net::UnixStream::connect(address())?;
    // A wedged agent must not hang the window.
    stream.set_read_timeout(Some(std::time::Duration::from_secs(15)))?;
    let reader = stream.try_clone()?;
    Ok((Box::new(reader), Box::new(stream)))
}

#[cfg(windows)]
fn connect() -> std::io::Result<Conn> {
    let pipe = std::fs::OpenOptions::new().read(true).write(true).open(address())?;
    let reader = pipe.try_clone()?;
    Ok((Box::new(reader), Box::new(pipe)))
}

fn send(w: &mut dyn Write, verb: &str, args: &Value) -> std::io::Result<()> {
    let mut line = serde_json::to_vec(&json!({ "verb": verb, "args": args }))?;
    line.push(b'\n');
    w.write_all(&line)?;
    w.flush()
}

fn parse(line: &str) -> Result<Value, ControlError> {
    let resp: Value = serde_json::from_str(line).map_err(|e| ControlError::internal(format!("malformed answer: {e}")))?;
    if resp.get("ok").and_then(Value::as_bool) == Some(true) {
        return Ok(resp.get("result").cloned().unwrap_or(Value::Null));
    }
    let e = resp.get("error").cloned().unwrap_or(Value::Null);
    let s = |k: &str| e.get(k).and_then(Value::as_str).map(str::to_owned);
    Err(ControlError {
        code: s("code").unwrap_or_else(|| "snk-99".into()),
        message: s("message").unwrap_or_else(|| "the agent refused".into()),
        kind: s("kind").unwrap_or_else(|| "internal error".into()),
        hint: s("hint"),
        cause: s("cause"),
    })
}

/// Sends one request and returns the agent's result.
pub fn call(verb: &str, args: Value) -> Result<Value, ControlError> {
    let (r, mut w) = connect().map_err(ControlError::not_running)?;
    send(&mut *w, verb, &args).map_err(ControlError::not_running)?;
    let mut line = String::new();
    BufReader::new(r)
        .read_line(&mut line)
        .map_err(|e| ControlError::internal(format!("reading the answer: {e}")))?;
    if line.is_empty() {
        return Err(ControlError::internal("the agent closed the connection without answering"));
    }
    parse(&line)
}

/// Follows the agent's status until the process ends: `on_status` gets every
/// status, and None whenever the agent stops answering. It reconnects on its
/// own, so a restarted agent shows up again without touching the window.
pub fn watch(mut on_status: impl FnMut(Option<Value>)) {
    let mut announced_down = false;
    loop {
        if let Ok((r, mut w)) = connect_for_events() {
            if send(&mut *w, "events", &Value::Null).is_ok() {
                announced_down = false;
                for line in BufReader::new(r).lines() {
                    match line.map(|l| parse(&l)) {
                        Ok(Ok(status)) => on_status(Some(status)),
                        _ => break,
                    }
                }
            }
        }
        if !announced_down {
            on_status(None);
            announced_down = true;
        }
        std::thread::sleep(std::time::Duration::from_secs(2));
    }
}

/// The events stream stays open indefinitely, so it has no read timeout.
#[cfg(unix)]
fn connect_for_events() -> std::io::Result<Conn> {
    let stream = std::os::unix::net::UnixStream::connect(address())?;
    let reader = stream.try_clone()?;
    Ok((Box::new(reader), Box::new(stream)))
}

#[cfg(windows)]
fn connect_for_events() -> std::io::Result<Conn> {
    connect()
}
