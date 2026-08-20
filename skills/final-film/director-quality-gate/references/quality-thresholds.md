# Quality thresholds

Candidate duration: requested 4 seconds with at most 1 second provider variance and at most 0.25 second normalization drift.

Attempts: 1 initial + 1 regeneration with the same provider. No provider substitution.

Full film: 90000-120000 ms, 1920x1080, CFR30, MP4/H.264/yuv420p/AAC, FFmpeg compositor status `ok`, requirements `satisfied` or `satisfied_with_warnings`, and no skipped planned operations.

Generated motion: aggregate black <=0.5s; unintended freeze <=1.0s. Full film: aggregate unexpected black <=0.5s; unintended freeze <=3.0s outside declared factual result holds. Audio present: -16 LUFS ±2, true peak <=-1 dBTP. Verified silence is valid.

Provider failures may have no candidate ID and score 0. A retry is counted only after a distinct persisted provider admission reaches a terminal failure; polling timeout resumes the same admission.
