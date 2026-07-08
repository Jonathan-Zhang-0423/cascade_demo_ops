export interface RecordRequest {
  graph: unknown;
  output_dir: string;
  viewport?: { width: number; height: number };
  headless?: boolean;
}

export interface RecordResult {
  recording_path: string;
  screenshot_paths: string[];
  trace_path?: string;
}

export async function record(request: RecordRequest): Promise<RecordResult> {
  const outputDir = request.output_dir || "artifacts/rehearsal";
  return {
    recording_path: `${outputDir}/recording.webm`,
    screenshot_paths: [`${outputDir}/step-001.png`],
    trace_path: `${outputDir}/trace.zip`,
  };
}
