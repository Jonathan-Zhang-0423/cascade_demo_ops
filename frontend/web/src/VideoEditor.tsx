import { useEffect, useMemo, useState } from "react";
import { createEditorClient } from "./editor";
import type { EditorPlan, EditorSession, EditorShot } from "./editor";

export function VideoEditor() {
  const client = useMemo(() => createEditorClient(), []);
  const [sessions, setSessions] = useState<EditorSession[]>([]);
  const [session, setSession] = useState<EditorSession>();
  const [draftPlan, setDraftPlan] = useState<EditorPlan>();
  const [selectedShotID, setSelectedShotID] = useState("");
  const [newName, setNewName] = useState("产品演示剪辑");
  const [sourcePath, setSourcePath] = useState("");
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    void refreshSessions();
  }, []);

  const selectedShot = draftPlan?.shots.find((shot) => shot.id === selectedShotID) ?? draftPlan?.shots[0];
  const selectedIndex = selectedShot ? draftPlan?.shots.findIndex((shot) => shot.id === selectedShot.id) ?? -1 : -1;
  const previewURL = session?.preview.status === "ready" && client.mode === "local" ? `${client.mediaURL(session.session_id, "preview")}?r=${session.preview.revision ?? session.revision}` : "";
  const sourceURL = !previewURL && session && selectedShot && client.mode === "local" ? client.mediaURL(session.session_id, "asset", selectedShot.source_artifact_id) : "";

  async function refreshSessions() {
    const result = await client.listSessions();
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "无法读取编辑项目");
      return;
    }
    setSessions(result.data);
    if (!session && result.data[0]) loadSession(result.data[0]);
  }

  function loadSession(next: EditorSession) {
    setSession(next);
    setDraftPlan(structuredClone(next.edit_plan));
    setSelectedShotID(next.edit_plan.shots[0]?.id ?? "");
    setMessage("");
  }

  async function createSession() {
    setBusy("create");
    const result = await client.createSession(newName.trim() || "未命名演示");
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "创建失败");
      return;
    }
    setSessions((current) => [result.data!, ...current]);
    loadSession(result.data);
  }

  async function importAsset() {
    if (!session || !sourcePath.trim()) return;
    setBusy("import");
    const result = await client.importAsset(session.session_id, sourcePath.trim());
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "素材导入失败");
      return;
    }
    loadSession(result.data);
    setSourcePath("");
    setMessage("素材已加入时间线。原始文件保持只读。 ");
  }

  function patchShot(patch: Partial<EditorShot>) {
    if (!draftPlan || !selectedShot) return;
    setDraftPlan({ ...draftPlan, shots: draftPlan.shots.map((shot) => (shot.id === selectedShot.id ? { ...shot, ...patch } : shot)) });
  }

  function patchRange(position: 0 | 1, seconds: number) {
    if (!selectedShot) return;
    const range: [number, number] = selectedShot.source_time_range_ms ? [...selectedShot.source_time_range_ms] : [0, 1000];
    range[position] = Math.max(0, Math.round(seconds * 1000));
    if (range[1] <= range[0]) range[1] = range[0] + 500;
    patchShot({ source_time_range_ms: range });
  }

  function patchCaption(text: string) {
    if (!selectedShot) return;
    const overlays = [...(selectedShot.overlays ?? [])];
    const captionIndex = overlays.findIndex((overlay) => overlay.type === "caption");
    if (!text.trim()) {
      patchShot({ overlays: overlays.filter((_, index) => index !== captionIndex) });
      return;
    }
    const caption = { type: "caption", text, start_ms: 0, end_ms: Math.min(3000, shotDuration(selectedShot)) };
    if (captionIndex >= 0) overlays[captionIndex] = { ...overlays[captionIndex], ...caption };
    else overlays.push(caption);
    patchShot({ overlays });
  }

  function moveShot(direction: -1 | 1) {
    if (!draftPlan || selectedIndex < 0) return;
    const target = selectedIndex + direction;
    if (target < 0 || target >= draftPlan.shots.length) return;
    const shots = [...draftPlan.shots];
    [shots[selectedIndex], shots[target]] = [shots[target]!, shots[selectedIndex]!];
    setDraftPlan({ ...draftPlan, shots });
  }

  function splitShot() {
    if (!draftPlan || !selectedShot?.source_time_range_ms) return;
    const [start, end] = selectedShot.source_time_range_ms;
    if (end-start < 1000) return;
    const middle = Math.round((start + end) / 2);
    const left = { ...selectedShot, source_time_range_ms: [start, middle] as [number, number] };
    const right = { ...selectedShot, id: `shot_${Date.now()}`, source_time_range_ms: [middle, end] as [number, number], purpose: `${selectedShot.purpose}（后半段）` };
    const shots = [...draftPlan.shots];
    shots.splice(selectedIndex, 1, left, right);
    setDraftPlan({ ...draftPlan, shots });
    setSelectedShotID(right.id);
  }

  function deleteShot() {
    if (!draftPlan || !selectedShot) return;
    const shots = draftPlan.shots.filter((shot) => shot.id !== selectedShot.id);
    setDraftPlan({ ...draftPlan, shots });
    setSelectedShotID(shots[0]?.id ?? "");
  }

  async function savePlan() {
    if (!session || !draftPlan) return;
    setBusy("save");
    const result = await client.savePlan(session.session_id, session.revision, draftPlan);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "保存失败");
      return;
    }
    loadSession(result.data);
    setMessage(result.data.validation?.valid ? "编辑计划已保存并通过校验。" : "计划已保存，但仍有校验问题。 ");
  }

  async function render(kind: "preview" | "final") {
    if (!session) return;
    setBusy(kind);
    const result = kind === "preview" ? await client.preview(session.session_id) : await client.render(session.session_id);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "渲染失败");
      return;
    }
    loadSession(result.data);
    setMessage(kind === "preview" ? "预览渲染完成。" : `成片已导出：${result.data.final_render.video_path ?? "本地输出目录"}`);
  }

  return (
    <div className="editor-shell">
      <header className="editor-toolbar">
        <div>
          <p className="eyebrow">Local Demo Editor · MVP</p>
          <h2>本地轻量视频编辑器</h2>
        </div>
        <div className="editor-actions">
          <button className="row-action" disabled={!session || busy !== ""} onClick={savePlan}>{busy === "save" ? "保存中" : "保存计划"}</button>
          <button className="row-action" disabled={!session || busy !== ""} onClick={() => render("preview")}>{busy === "preview" ? "渲染中" : "生成预览"}</button>
          <button className="primary-action" disabled={!session || busy !== ""} onClick={() => render("final")}>{busy === "final" ? "导出中" : "导出 MP4"}</button>
        </div>
      </header>

      <div className="editor-project-row">
        <select value={session?.session_id ?? ""} onChange={(event) => { const next = sessions.find((item) => item.session_id === event.target.value); if (next) loadSession(next); }}>
          <option value="">选择编辑项目</option>
          {sessions.map((item) => <option key={item.session_id} value={item.session_id}>{item.name} · r{item.revision}</option>)}
        </select>
        <input value={newName} onChange={(event) => setNewName(event.target.value)} placeholder="新项目名称" />
        <button className="row-action" disabled={busy !== ""} onClick={createSession}>{busy === "create" ? "创建中" : "新建编辑项目"}</button>
      </div>

      {session ? (
        <>
          <div className="editor-grid">
            <section className="editor-pane media-bin">
              <div className="editor-pane-title"><strong>素材</strong><span>{session.asset_catalog.artifacts.length}</span></div>
              <div className="editor-import-row">
                <input value={sourcePath} onChange={(event) => setSourcePath(event.target.value)} placeholder="输入 Server 本机视频路径，例如 D:\\recordings\\demo.mp4" />
                <button className="row-action" disabled={!sourcePath.trim() || busy !== ""} onClick={importAsset}>{busy === "import" ? "探测中" : "导入"}</button>
              </div>
              <div className="editor-asset-list">
                {session.asset_catalog.artifacts.map((asset) => (
                  <button key={asset.id} className="editor-asset-card" onClick={() => setSelectedShotID(draftPlan?.shots.find((shot) => shot.source_artifact_id === asset.id)?.id ?? "") }>
                    <strong>{asset.label ?? asset.id}</strong>
                    <span>{formatDuration(asset.duration_ms ?? 0)} · {asset.mime_type ?? "video"}</span>
                    <small>{asset.metadata?.width ? `${asset.metadata.width}×${asset.metadata.height}` : "待探测"}</small>
                  </button>
                ))}
                {session.asset_catalog.artifacts.length === 0 ? <p className="editor-empty">导入浏览器录屏或本地视频后，系统会自动创建第一个片段。</p> : null}
              </div>
              <SeedanceCapability session={session} />
            </section>

            <section className="editor-pane preview-pane">
              <div className="editor-pane-title"><strong>预览</strong><span>{session.preview_profile.width}×{session.preview_profile.height}</span></div>
              <div className="video-stage">
                {previewURL || sourceURL ? <video key={previewURL || sourceURL} controls src={previewURL || sourceURL} /> : <div className="video-placeholder">选择素材后可预览；保存计划后生成低清预览。</div>}
              </div>
              <div className="render-status-row">
                <span>预览：{renderStatusLabel(session.preview.status)}</span>
                <span>成片：{renderStatusLabel(session.final_render.status)}</span>
              </div>
            </section>

            <section className="editor-pane inspector-pane">
              <div className="editor-pane-title"><strong>片段属性</strong><span>{selectedIndex >= 0 ? selectedIndex + 1 : 0}/{draftPlan?.shots.length ?? 0}</span></div>
              {selectedShot ? (
                <div className="editor-form">
                  <label><span>片段说明</span><input value={selectedShot.purpose} onChange={(event) => patchShot({ purpose: event.target.value })} /></label>
                  <div className="editor-form-pair">
                    <label><span>入点（秒）</span><input type="number" min="0" step="0.1" value={(selectedShot.source_time_range_ms?.[0] ?? 0) / 1000} onChange={(event) => patchRange(0, Number(event.target.value))} /></label>
                    <label><span>出点（秒）</span><input type="number" min="0.5" step="0.1" value={(selectedShot.source_time_range_ms?.[1] ?? 1000) / 1000} onChange={(event) => patchRange(1, Number(event.target.value))} /></label>
                  </div>
                  <label><span>中文字幕</span><textarea rows={4} value={captionText(selectedShot)} onChange={(event) => patchCaption(event.target.value)} placeholder="输入需要烧录到画面的字幕" /></label>
                  <div className="editor-clip-actions">
                    <button onClick={() => moveShot(-1)}>前移</button><button onClick={() => moveShot(1)}>后移</button><button onClick={splitShot}>居中分割</button><button className="danger-action" onClick={deleteShot}>删除</button>
                  </div>
                </div>
              ) : <p className="editor-empty">选择时间线片段后编辑属性。</p>}
            </section>
          </div>

          <section className="timeline-pane">
            <div className="editor-pane-title"><strong>时间线</strong><span>{formatDuration(planDuration(draftPlan))}</span></div>
            <div className="timeline-track">
              {(draftPlan?.shots ?? []).map((shot, index) => (
                <button key={shot.id} className={shot.id === selectedShot?.id ? "timeline-clip selected" : "timeline-clip"} style={{ flexGrow: Math.max(1, shotDuration(shot) / 1000) }} onClick={() => setSelectedShotID(shot.id)}>
                  <small>{index + 1}</small><strong>{shot.purpose}</strong><span>{formatDuration(shotDuration(shot))}</span>
                </button>
              ))}
              {(draftPlan?.shots.length ?? 0) === 0 ? <div className="timeline-empty">时间线为空</div> : null}
            </div>
            <div className="caption-track">
              {(draftPlan?.shots ?? []).map((shot) => <div key={shot.id}>{captionText(shot) || "·"}</div>)}
            </div>
          </section>
        </>
      ) : <div className="editor-empty-state">新建一个编辑项目，然后导入本地录屏。</div>}

      {message ? <div className="editor-message">{message}</div> : null}
    </div>
  );
}

function SeedanceCapability({ session }: { session: EditorSession }) {
  const capability = session.provider_capabilities.find((item) => item.provider === "seedance");
  if (!capability) return null;
  return (
    <div className="seedance-card">
      <div><strong>Seedance 扩展槽</strong><span>{capability.configured ? capability.mode : "未配置"}</span></div>
      <p>后续生成的视频只作为候选展示素材，必须人工审查后入轨，不会自动替代真实产品录屏。</p>
      <small>{capability.model || "模型待配置"} · auto_include=false</small>
    </div>
  );
}

function shotDuration(shot: EditorShot): number {
  return shot.source_time_range_ms ? Math.max(0, shot.source_time_range_ms[1] - shot.source_time_range_ms[0]) : 0;
}

function planDuration(plan?: EditorPlan): number {
  return plan?.shots.reduce((total, shot) => total + shotDuration(shot), 0) ?? 0;
}

function captionText(shot: EditorShot): string {
  return shot.overlays?.find((overlay) => overlay.type === "caption")?.text ?? "";
}

function formatDuration(valueMS: number): string {
  const seconds = Math.max(0, Math.round(valueMS / 100) / 10);
  return `${seconds.toFixed(seconds % 1 === 0 ? 0 : 1)}s`;
}

function renderStatusLabel(status: string): string {
  return ({ not_started: "未开始", running: "渲染中", ready: "已就绪", failed: "失败" } as Record<string, string>)[status] ?? status;
}
