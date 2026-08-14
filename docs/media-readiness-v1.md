# Media Readiness v1

`backend/cmd/mediareadiness` performs a local configuration check before a
real media run. It loads the same repository `.env` search path as the Server,
but never contacts TOS, Doubao, Seedance, or any other provider. It also does
not upload assets, start Chromium, or change an edit plan.

Run it from `backend`:

```powershell
$env:CASCADE_FFMPEG_PATH = "D:\ffmpeg\bin\ffmpeg.exe"
$env:CASCADE_FFPROBE_PATH = "D:\Engine-7-8\.tooling\ffprobe\node_modules\ffprobe-static\bin\win32\x64\ffprobe.exe"
go run ./cmd/mediareadiness
```

The report is safe to retain in diagnostics: credential values and secret
paths are never printed. `video_ready` checks the required FFmpeg, FFprobe,
real Ark mode, Seedance, and private TOS configuration. `tts_ready` reports
whether the optional Doubao OpenSpeech narration fields are complete. Missing
TTS does not block a no-narration video acceptance; `ready` is an alias for
`video_ready`.

This command is a preparation/readiness signal, not proof of a provider call.
Use `tospreflight` and `ttspreflight` only after the corresponding external
call is explicitly approved, and keep their candidate outputs review-only.

On a successful `ttspreflight`, the Server writes a redacted
`cascade.audio_candidate_audit.v1` record. It proves whether a real call was
made and whether download and FFprobe validation passed. It deliberately omits
the local path, synthesis text, transcript, credentials, and provider URL. A
successful candidate remains `provider_output_adopted=false` and
`inserted_into_demo_edit_plan=false` until a separate explicit approval step.
