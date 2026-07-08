export interface RenderRequest {
  graph: unknown;
  recording_paths: string[];
  output_dir: string;
  duration_sec: number;
}

export interface RenderResult {
  video_path: string;
  step_by_step_docs_path: string;
}

export async function render(request: RenderRequest): Promise<RenderResult> {
  const outputDir = request.output_dir || "artifacts/rendered";
  const duration = request.duration_sec || 60;
  return {
    video_path: `${outputDir}/demo_${duration}s.mp4`,
    step_by_step_docs_path: `${outputDir}/step_by_step.md`,
  };
}
