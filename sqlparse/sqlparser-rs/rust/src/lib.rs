//! C ABI bridge over `sqlparser` (sqlparser-rs).
//!
//! Exposes a single entry point, `sqlkit_parse`, which parses a SQL string with
//! a named dialect and returns a JSON document on the heap. The JSON is either
//! `{"statements": [ <ast> ... ]}` on success or `{"error": "<message>"}` on
//! failure. Ownership of the returned buffer transfers to the caller, who must
//! release it with `sqlkit_string_free`.

use std::ffi::{c_char, CStr, CString};
use std::panic;

use sqlparser::dialect::{dialect_from_str, GenericDialect};
use sqlparser::parser::Parser;

/// Build a heap C string, returning a null pointer if the value contains an
/// interior NUL byte (which JSON never does).
fn into_c_string(value: String) -> *mut c_char {
    match CString::new(value) {
        Ok(s) => s.into_raw(),
        Err(_) => std::ptr::null_mut(),
    }
}

/// Serialize an error message into the `{"error": ...}` envelope.
fn error_json(message: &str) -> *mut c_char {
    let body = serde_json::json!({ "error": message });
    into_c_string(body.to_string())
}

/// Parse `sql` using the dialect named by `dialect` (e.g. "postgresql",
/// "mysql", "sqlite", "ansi", "generic"). Returns a JSON document; see the
/// module docs for its shape. Both arguments must be valid NUL-terminated
/// UTF-8 C strings. The returned pointer must be freed with
/// `sqlkit_string_free`.
///
/// # Safety
/// `dialect` and `sql` must be valid pointers to NUL-terminated C strings.
#[no_mangle]
pub unsafe extern "C" fn sqlkit_parse(
    dialect: *const c_char,
    sql: *const c_char,
) -> *mut c_char {
    if dialect.is_null() || sql.is_null() {
        return error_json("null argument");
    }

    let dialect_name = match CStr::from_ptr(dialect).to_str() {
        Ok(s) => s,
        Err(_) => return error_json("dialect is not valid UTF-8"),
    };
    let sql_text = match CStr::from_ptr(sql).to_str() {
        Ok(s) => s,
        Err(_) => return error_json("sql is not valid UTF-8"),
    };

    // sqlparser can panic on some malformed inputs; contain it so we never
    // unwind across the FFI boundary.
    let result = panic::catch_unwind(|| parse_to_json(dialect_name, sql_text));
    match result {
        Ok(Ok(json)) => into_c_string(json),
        Ok(Err(message)) => error_json(&message),
        Err(_) => error_json("internal parser panic"),
    }
}

fn parse_to_json(dialect_name: &str, sql_text: &str) -> Result<String, String> {
    let dialect = resolve_dialect(dialect_name);
    let statements = Parser::parse_sql(dialect.as_ref(), sql_text).map_err(|e| e.to_string())?;
    let body = serde_json::json!({ "statements": statements });
    serde_json::to_string(&body).map_err(|e| e.to_string())
}

fn resolve_dialect(name: &str) -> Box<dyn sqlparser::dialect::Dialect> {
    match name.trim().to_ascii_lowercase().as_str() {
        "" | "generic" => Box::new(GenericDialect {}),
        // Accept a few aliases that sqlparser's dialect_from_str does not.
        "postgres" | "postgresql" => dialect_from_str("postgresql").unwrap(),
        "sqlite" | "sqlite3" => dialect_from_str("sqlite").unwrap(),
        other => dialect_from_str(other).unwrap_or_else(|| Box::new(GenericDialect {})),
    }
}

/// Free a string previously returned by `sqlkit_parse`. Passing null is a
/// no-op. Passing any other pointer not produced by this library, or freeing
/// twice, is undefined behavior.
///
/// # Safety
/// `ptr` must be a pointer returned by `sqlkit_parse` and not yet freed.
#[no_mangle]
pub unsafe extern "C" fn sqlkit_string_free(ptr: *mut c_char) {
    if !ptr.is_null() {
        drop(CString::from_raw(ptr));
    }
}
