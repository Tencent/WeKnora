//! C ABI surface for the Go bindings.
//!
//! Memory ownership rule: **Rust allocates, C ABI exposes a `_free` for every
//! allocation family, Go releases handles.** There are two allocation
//! families:
//!
//! - strings: `*mut c_char` + length out-params, returned by
//!   `anydoc_to_markdown*` and `anydoc_last_error`. Released via
//!   `anydoc_string_free`. Length is passed separately so binary-safe.
//! - document buffer: a single `*mut u8` + length, returned by
//!   `anydoc_to_document`, holding the flat serialization from [`model`].
//!   Released via `anydoc_buffer_free`.
//!
//! Errors surface as stable non-zero codes (see `ERR_*`); the human-readable
//! message is available through `anydoc_last_error`, thread-local.

#![allow(clippy::missing_safety_doc)]

use std::ffi::CString;
use std::os::raw::{c_char, c_int};
use std::ptr;

use anydoc::{ConvertError, Format, document_to_markdown};

mod asset_links;
mod model;

// ---- Error codes -----------------------------------------------------------

/// Success.
pub const ERR_OK: c_int = 0;
/// `ConvertError::Unsupported`.
pub const ERR_UNSUPPORTED: c_int = 1;
/// `ConvertError::Malformed`.
pub const ERR_MALFORMED: c_int = 2;
/// `ConvertError::Encrypted`.
pub const ERR_ENCRYPTED: c_int = 3;
/// `ConvertError::ResourceLimit`.
pub const ERR_RESOURCE_LIMIT: c_int = 4;
/// `ConvertError::MissingPart`.
pub const ERR_MISSING_PART: c_int = 5;
/// `ConvertError::Io`.
pub const ERR_IO: c_int = 6;
/// PDF was passed to `anydoc_to_document` (unsupported there).
pub const ERR_PDF_NO_MODEL: c_int = 7;
/// Null pointer or invalid argument passed to the ABI.
pub const ERR_INVALID_ARG: c_int = 8;
/// Unknown format name passed to the ABI.
pub const ERR_UNKNOWN_FORMAT: c_int = 9;

thread_local! {
    static LAST_ERROR: std::cell::RefCell<Option<CString>> = const { std::cell::RefCell::new(None) };
}

fn set_last_error(msg: &str) {
    let cstring = CString::new(msg).unwrap_or_else(|_| CString::new("<invalid utf-8>").unwrap());
    LAST_ERROR.with(|cell| *cell.borrow_mut() = Some(cstring));
}

fn error_code(err: &ConvertError) -> c_int {
    match err {
        ConvertError::Unsupported(_) => ERR_UNSUPPORTED,
        ConvertError::Malformed { .. } => ERR_MALFORMED,
        ConvertError::Encrypted => ERR_ENCRYPTED,
        ConvertError::ResourceLimit { .. } => ERR_RESOURCE_LIMIT,
        ConvertError::MissingPart { .. } => ERR_MISSING_PART,
        ConvertError::Io(_) => ERR_IO,
        _ => ERR_UNSUPPORTED,
    }
}

fn fail(err: ConvertError) -> c_int {
    let code = error_code(&err);
    set_last_error(&err.to_string());
    code
}

/// Run a conversion with panics contained. A panic that escapes an
/// `extern "C"` function aborts the process (Rust 1.81+), so one malformed
/// document would take the whole host process down instead of failing a single
/// conversion. A caught panic surfaces as `Malformed`, the same as any other
/// unusable input.
///
/// This cannot contain an abort the runtime raises on its own (stack overflow,
/// allocation failure); those still kill the process.
fn guarded<T>(
    entry: &str,
    parse: impl FnOnce() -> Result<T, ConvertError>,
) -> Result<T, ConvertError> {
    // AssertUnwindSafe: the closure only reads the input slice and returns an
    // owned result. Nothing observable is left half-written when it panics,
    // because the out-params are written by the caller after this returns.
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(parse)) {
        Ok(result) => result,
        Err(_) => Err(ConvertError::Malformed {
            part: None,
            detail: format!("panic while converting in {entry}"),
        }),
    }
}

// ---- Format enum -----------------------------------------------------------

/// C-side format tag. Stable; mirrors the Node/Python lowercase string names
/// via `format_name`. `ANYDOC_FORMAT_NONE` is the `Option::None` sentinel.
pub const ANYDOC_FORMAT_NONE: c_int = -1;
pub const ANYDOC_FORMAT_DOC: c_int = 0;
pub const ANYDOC_FORMAT_DOCX: c_int = 1;
pub const ANYDOC_FORMAT_ODT: c_int = 2;
pub const ANYDOC_FORMAT_PDF: c_int = 3;
pub const ANYDOC_FORMAT_PPT: c_int = 4;
pub const ANYDOC_FORMAT_PPTX: c_int = 5;
pub const ANYDOC_FORMAT_RTF: c_int = 6;
pub const ANYDOC_FORMAT_EPUB: c_int = 7;
pub const ANYDOC_FORMAT_XLSX: c_int = 8;
pub const ANYDOC_FORMAT_ODS: c_int = 9;
pub const ANYDOC_FORMAT_ODP: c_int = 10;
pub const ANYDOC_FORMAT_CSV: c_int = 11;

fn format_to_c(format: Format) -> c_int {
    match format {
        Format::Doc => ANYDOC_FORMAT_DOC,
        Format::Docx => ANYDOC_FORMAT_DOCX,
        Format::Odt => ANYDOC_FORMAT_ODT,
        Format::Pdf => ANYDOC_FORMAT_PDF,
        Format::Ppt => ANYDOC_FORMAT_PPT,
        Format::Pptx => ANYDOC_FORMAT_PPTX,
        Format::Rtf => ANYDOC_FORMAT_RTF,
        Format::Epub => ANYDOC_FORMAT_EPUB,
        Format::Excel => ANYDOC_FORMAT_XLSX,
        Format::Ods => ANYDOC_FORMAT_ODS,
        Format::Odp => ANYDOC_FORMAT_ODP,
        Format::Csv => ANYDOC_FORMAT_CSV,
    }
}

fn format_from_c(tag: c_int) -> Option<Format> {
    Some(match tag {
        ANYDOC_FORMAT_DOC => Format::Doc,
        ANYDOC_FORMAT_DOCX => Format::Docx,
        ANYDOC_FORMAT_ODT => Format::Odt,
        ANYDOC_FORMAT_PDF => Format::Pdf,
        ANYDOC_FORMAT_PPT => Format::Ppt,
        ANYDOC_FORMAT_PPTX => Format::Pptx,
        ANYDOC_FORMAT_RTF => Format::Rtf,
        ANYDOC_FORMAT_EPUB => Format::Epub,
        ANYDOC_FORMAT_XLSX => Format::Excel,
        ANYDOC_FORMAT_ODS => Format::Ods,
        ANYDOC_FORMAT_ODP => Format::Odp,
        ANYDOC_FORMAT_CSV => Format::Csv,
        _ => return None,
    })
}

/// Format names, lowercase, matching the Node/Python convention.
const FORMAT_NAMES: [&str; 12] =
    ["doc", "docx", "odt", "pdf", "ppt", "pptx", "rtf", "epub", "xlsx", "ods", "odp", "csv"];

fn format_name(format: Format) -> &'static str {
    match format {
        Format::Doc => FORMAT_NAMES[0],
        Format::Docx => FORMAT_NAMES[1],
        Format::Odt => FORMAT_NAMES[2],
        Format::Pdf => FORMAT_NAMES[3],
        Format::Ppt => FORMAT_NAMES[4],
        Format::Pptx => FORMAT_NAMES[5],
        Format::Rtf => FORMAT_NAMES[6],
        Format::Epub => FORMAT_NAMES[7],
        Format::Excel => FORMAT_NAMES[8],
        Format::Ods => FORMAT_NAMES[9],
        Format::Odp => FORMAT_NAMES[10],
        Format::Csv => FORMAT_NAMES[11],
    }
}

fn parse_format_name(name: &str) -> Option<Format> {
    FORMAT_NAMES
        .iter()
        .zip([
            Format::Doc,
            Format::Docx,
            Format::Odt,
            Format::Pdf,
            Format::Ppt,
            Format::Pptx,
            Format::Rtf,
            Format::Epub,
            Format::Excel,
            Format::Ods,
            Format::Odp,
            Format::Csv,
        ])
        .find(|(n, _)| **n == name)
        .map(|(_, f)| f)
}

// ---- String out-param helper ----------------------------------------------

/// Box a `String` into a heap allocation the caller owns and must free with
/// `anydoc_string_free`. Writes the byte length (excluding any NUL) to
/// `*out_len`. The buffer is NUL-terminated for safety with C string readers,
/// but binary-safe content relies on `out_len`.
///
/// Returns a dangling null pointer for empty strings so the Go side can treat
/// a null result as "empty" without needing to free it. Length is still
/// written as 0.
fn string_out(s: String, out_str: *mut *mut c_char, out_len: *mut usize) -> c_int {
    if out_str.is_null() {
        set_last_error("anydoc: out_str is null");
        return ERR_INVALID_ARG;
    }
    let len = s.len();
    // SAFETY: we write the length unconditionally; null is the empty sentinel.
    if !out_len.is_null() {
        unsafe { *out_len = len };
    }
    if len == 0 {
        // Empty string: return null sentinel, no allocation to free.
        unsafe { *out_str = ptr::null_mut() };
        return ERR_OK;
    }
    let cstring = match CString::new(s.into_bytes()) {
        Ok(cs) => cs,
        Err(_) => {
            // Contains an interior NUL: fall back to lossy replacement so the
            // caller still gets something. Markdown shouldn't contain NULs.
            set_last_error("anydoc: output contained an interior NUL byte");
            return ERR_INVALID_ARG;
        }
    };
    let raw = cstring.into_raw();
    // into_raw returns a buffer of capacity len+1 (with the trailing NUL).
    unsafe { *out_str = raw };
    ERR_OK
}

// ---- C ABI functions -------------------------------------------------------

/// Detect the format from the content. Writes a format tag to `*out` (one of
/// `ANYDOC_FORMAT_*`), or `ANYDOC_FORMAT_NONE` when nothing matches.
///
/// Returns `ERR_OK` on success (including no-match), `ERR_INVALID_ARG` on null
/// pointers.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_format_from_bytes(
    bytes: *const u8,
    len: usize,
    out: *mut c_int,
) -> c_int {
    if bytes.is_null() || out.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_format_from_bytes");
        return ERR_INVALID_ARG;
    }
    let slice = unsafe { std::slice::from_raw_parts(bytes, len) };
    let tag = Format::from_bytes(slice).map_or(ANYDOC_FORMAT_NONE, format_to_c);
    unsafe { *out = tag };
    ERR_OK
}

/// The format a bare extension names (no leading dot). Writes a format tag to
/// `*out`, or `ANYDOC_FORMAT_NONE` when unrecognized.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_format_from_extension(
    ext: *const c_char,
    out: *mut c_int,
) -> c_int {
    if ext.is_null() || out.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_format_from_extension");
        return ERR_INVALID_ARG;
    }
    let ext_str = match unsafe { std::ffi::CStr::from_ptr(ext) }.to_str() {
        Ok(s) => s.trim_start_matches('.'),
        Err(_) => {
            set_last_error("anydoc: extension is not valid UTF-8");
            return ERR_INVALID_ARG;
        }
    };
    let tag = Format::from_extension(ext_str).map_or(ANYDOC_FORMAT_NONE, format_to_c);
    unsafe { *out = tag };
    ERR_OK
}

/// The format a path's extension names. Writes a format tag to `*out`, or
/// `ANYDOC_FORMAT_NONE` when unrecognized.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_format_from_path(path: *const c_char, out: *mut c_int) -> c_int {
    if path.is_null() || out.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_format_from_path");
        return ERR_INVALID_ARG;
    }
    let path_str = match unsafe { std::ffi::CStr::from_ptr(path) }.to_str() {
        Ok(s) => s,
        Err(_) => {
            set_last_error("anydoc: path is not valid UTF-8");
            return ERR_INVALID_ARG;
        }
    };
    let tag =
        Format::from_path(std::path::Path::new(path_str)).map_or(ANYDOC_FORMAT_NONE, format_to_c);
    unsafe { *out = tag };
    ERR_OK
}

/// Convert a document file to Markdown. The format is detected from the file
/// content, with the extension as the fallback for signature-less formats
/// (CSV).
///
/// On success writes a `*mut c_char` to `*out_str` and its byte length to
/// `*out_len`. The caller must free the string with `anydoc_string_free`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_to_markdown(
    path: *const c_char,
    out_str: *mut *mut c_char,
    out_len: *mut usize,
) -> c_int {
    if path.is_null() || out_str.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_to_markdown");
        return ERR_INVALID_ARG;
    }
    let path_str = match unsafe { std::ffi::CStr::from_ptr(path) }.to_str() {
        Ok(s) => s,
        Err(_) => {
            set_last_error("anydoc: path is not valid UTF-8");
            return ERR_INVALID_ARG;
        }
    };
    match guarded("anydoc_to_markdown", || anydoc::to_markdown(path_str)) {
        Ok(md) => string_out(md, out_str, out_len),
        Err(e) => fail(e),
    }
}

/// Convert an in-memory document to Markdown. `format_tag` is one of the
/// `ANYDOC_FORMAT_*` constants, or `ANYDOC_FORMAT_NONE` to detect from the
/// content.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_to_markdown_bytes(
    bytes: *const u8,
    len: usize,
    format_tag: c_int,
    out_str: *mut *mut c_char,
    out_len: *mut usize,
) -> c_int {
    if bytes.is_null() || out_str.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_to_markdown_bytes");
        return ERR_INVALID_ARG;
    }
    let format = if format_tag == ANYDOC_FORMAT_NONE {
        None
    } else {
        match format_from_c(format_tag) {
            Some(f) => Some(f),
            None => {
                set_last_error("anydoc: unknown format tag");
                return ERR_UNKNOWN_FORMAT;
            }
        }
    };
    let slice = unsafe { std::slice::from_raw_parts(bytes, len) };
    match guarded("anydoc_to_markdown_bytes", || anydoc::to_markdown_bytes(slice, format)) {
        Ok(md) => string_out(md, out_str, out_len),
        Err(e) => fail(e),
    }
}

/// Convert an in-memory document to Markdown with embedded images rewritten
/// as `![alt](images/image-N.ext)` so they keep their original positions.
///
/// This is `to_document` + anydoc's official GFM serializer. Asset images are
/// first turned into `ImageSource::External` URLs; the serializer already
/// emits those as ordinary Markdown image links. PDF has no document model
/// and is converted the same way as `anydoc_to_markdown_bytes`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_to_markdown_with_asset_links(
    bytes: *const u8,
    len: usize,
    format_tag: c_int,
    out_str: *mut *mut c_char,
    out_len: *mut usize,
) -> c_int {
    if bytes.is_null() || out_str.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_to_markdown_with_asset_links");
        return ERR_INVALID_ARG;
    }
    let format = if format_tag == ANYDOC_FORMAT_NONE {
        None
    } else {
        match format_from_c(format_tag) {
            Some(f) => Some(f),
            None => {
                set_last_error("anydoc: unknown format tag");
                return ERR_UNKNOWN_FORMAT;
            }
        }
    };
    let slice = unsafe { std::slice::from_raw_parts(bytes, len) };
    match guarded("anydoc_to_markdown_with_asset_links", || {
        markdown_with_asset_links(slice, format)
    }) {
        Ok(md) => string_out(md, out_str, out_len),
        Err(e) => fail(e),
    }
}

fn markdown_with_asset_links(
    bytes: &[u8],
    format: Option<Format>,
) -> Result<String, ConvertError> {
    let resolved = format
        .or_else(|| Format::from_bytes(bytes))
        .ok_or_else(|| {
            ConvertError::Unsupported(
                "unrecognized file content: name the format explicitly".into(),
            )
        })?;
    if resolved == Format::Pdf {
        return anydoc::to_markdown_bytes(bytes, resolved);
    }
    let mut document = anydoc::to_document(bytes, resolved)?;
    asset_links::rewrite_asset_images(&mut document);
    Ok(document_to_markdown(&document))
}

/// Parse an in-memory document into the document model. `format_tag` is one of
/// the `ANYDOC_FORMAT_*` constants, or `ANYDOC_FORMAT_NONE` to detect from the
/// content.
///
/// On success writes a heap-allocated byte buffer to `*out_buf` and its length
/// to `*out_len`. The buffer holds the flat serialization (see `model`). The
/// caller must free it with `anydoc_buffer_free`.
///
/// Returns `ERR_PDF_NO_MODEL` for PDF input: PDF conversion produces Markdown
/// directly and has no document-model form. Use `anydoc_to_markdown_bytes`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_to_document(
    bytes: *const u8,
    len: usize,
    format_tag: c_int,
    out_buf: *mut *mut u8,
    out_len: *mut usize,
) -> c_int {
    if bytes.is_null() || out_buf.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_to_document");
        return ERR_INVALID_ARG;
    }
    let format = if format_tag == ANYDOC_FORMAT_NONE {
        None
    } else {
        match format_from_c(format_tag) {
            Some(f) => Some(f),
            None => {
                set_last_error("anydoc: unknown format tag");
                return ERR_UNKNOWN_FORMAT;
            }
        }
    };
    let slice = unsafe { std::slice::from_raw_parts(bytes, len) };
    // Resolve the format up front so we can give the typed PDF error before
    // parsing, matching the crate's `to_document` contract.
    let resolved = match format {
        Some(f) => f,
        None => match Format::from_bytes(slice) {
            Some(f) => f,
            None => {
                set_last_error(
                    "unsupported input: unrecognized file content: name the format explicitly",
                );
                return ERR_UNSUPPORTED;
            }
        },
    };
    if resolved == Format::Pdf {
        set_last_error(
            "unsupported input: PDF conversion produces Markdown directly and has no document-model form; use to_markdown_bytes",
        );
        return ERR_PDF_NO_MODEL;
    }
    let document = match guarded("anydoc_to_document", || anydoc::to_document(slice, resolved)) {
        Ok(d) => d,
        Err(e) => return fail(e),
    };
    let mut encoder = model::Encoder::new();
    model::write_document(&mut encoder, &document);
    let body = encoder.into_vec();
    let len = body.len();
    // Layout: [usize len][usize cap][u8 data..len]. The caller receives a
    // pointer to the data area and the length; `anydoc_buffer_free` walks back
    // to the header to recover (len, cap) for a sound deallocation.
    let header_size = std::mem::size_of::<BufferHeader>();
    let total = header_size + len;
    let layout = std::alloc::Layout::from_size_align(total, std::mem::align_of::<BufferHeader>())
        .expect("document buffer layout");
    let base = unsafe { std::alloc::alloc(layout) as *mut BufferHeader };
    if base.is_null() {
        set_last_error("anydoc: out of memory");
        return ERR_IO;
    }
    // The capacity we record is the *total* allocation size, so the free path
    // can reconstruct a layout of the same size to pass back to `dealloc`.
    unsafe {
        std::ptr::write(base, BufferHeader { len, cap: total });
        let data = (base as *mut u8).add(header_size);
        std::ptr::copy_nonoverlapping(body.as_ptr(), data, len);
        *out_buf = data;
        if !out_len.is_null() {
            *out_len = len;
        }
    }
    // `body` drops here, freeing its own backing allocation.
    ERR_OK
}

/// Header prepended to every document buffer, so `anydoc_buffer_free` can
/// soundly reclaim the allocation without any thread-local state. The data
/// the caller sees starts immediately after this header.
#[repr(C)]
struct BufferHeader {
    len: usize,
    /// Total allocation size (header + data), used to rebuild the Layout for
    /// `dealloc`.
    cap: usize,
}

/// Free a string returned by `anydoc_to_markdown*` or `anydoc_last_error`.
/// Null is a no-op (the empty-string sentinel).
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_string_free(s: *mut c_char) {
    if s.is_null() {
        return;
    }
    // SAFETY: the pointer was produced by `CString::into_raw`; reclaiming it
    // with `from_raw` is the matching deallocator.
    unsafe {
        let _ = CString::from_raw(s);
    }
}

/// Free a document buffer returned by `anydoc_to_document`. Null is a no-op.
/// `len` must be the length written to `*out_len` at allocation time; it is
/// validated against the header for safety.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_buffer_free(buf: *mut u8, len: usize) {
    if buf.is_null() {
        return;
    }
    let header_size = std::mem::size_of::<BufferHeader>();
    // SAFETY: the pointer was produced by `anydoc_to_document` with a
    // `BufferHeader` immediately before it. Walk back to read the header and
    // rebuild the Layout, then dealloc the whole block.
    unsafe {
        let base = (buf.sub(header_size)) as *mut BufferHeader;
        let header = base.read();
        if header.len != len {
            // Length mismatch: caller bug. Leak rather than risk a bad free.
            set_last_error("anydoc: anydoc_buffer_free length does not match allocation");
            return;
        }
        let layout = std::alloc::Layout::from_size_align_unchecked(
            header.cap,
            std::mem::align_of::<BufferHeader>(),
        );
        std::alloc::dealloc(base as *mut u8, layout);
    }
}

/// Return the human-readable message for the last error on the current
/// thread. The returned `*mut c_char` is freshly allocated and must be freed
/// with `anydoc_string_free`. Returns null when there is no last error.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_last_error() -> *mut c_char {
    LAST_ERROR
        .with(|cell| cell.borrow_mut().take())
        .and_then(|cstring| {
            // Re-allocate as a fresh owned C string the caller can free.
            let bytes = cstring.into_bytes_with_nul();
            CString::from_vec_with_nul(bytes).ok().map(|cs| cs.into_raw())
        })
        .unwrap_or(ptr::null_mut())
}

// ---- Go-side helpers exposed for parity tests -----------------------------

/// Return the lowercase name of a format tag, or null for an unknown tag.
/// The returned string must be freed with `anydoc_string_free`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_format_name(tag: c_int) -> *mut c_char {
    if tag == ANYDOC_FORMAT_NONE {
        return ptr::null_mut();
    }
    match format_from_c(tag) {
        Some(f) => {
            let name = format_name(f);
            match CString::new(name) {
                Ok(cs) => cs.into_raw(),
                Err(_) => ptr::null_mut(),
            }
        }
        None => ptr::null_mut(),
    }
}

/// Parse a lowercase format name into a tag. Writes the tag to `*out`, or
/// `ANYDOC_FORMAT_NONE` when the name is unknown.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn anydoc_format_from_name(name: *const c_char, out: *mut c_int) -> c_int {
    if name.is_null() || out.is_null() {
        set_last_error("anydoc: null pointer passed to anydoc_format_from_name");
        return ERR_INVALID_ARG;
    }
    let name_str = match unsafe { std::ffi::CStr::from_ptr(name) }.to_str() {
        Ok(s) => s,
        Err(_) => {
            set_last_error("anydoc: format name is not valid UTF-8");
            return ERR_INVALID_ARG;
        }
    };
    let tag = parse_format_name(name_str).map_or(ANYDOC_FORMAT_NONE, format_to_c);
    unsafe { *out = tag };
    ERR_OK
}
