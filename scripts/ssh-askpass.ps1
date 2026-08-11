$ErrorActionPreference = "Stop"

if ([string]::IsNullOrEmpty($env:CASCADE_SSH_BOOTSTRAP_PASSWORD)) {
    exit 1
}

[Console]::Out.WriteLine($env:CASCADE_SSH_BOOTSTRAP_PASSWORD)
