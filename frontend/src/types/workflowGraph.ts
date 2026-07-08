export type DemoWorkflowGraph = {
  id: string;
  version: number;
  entry_point: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  assets: AssetManifest;
};

export type GraphNode = {
  id: string;
  action: string;
  selector: string;
  input_data: string;
  expected_outcome: string;
  is_screenshot: boolean;
  has_zoom: boolean;
  retry_policy: number;
};

export type GraphEdge = {
  id: string;
  from_node: string;
  to_node: string;
  condition?: string;
};

export type AssetManifest = {
  demo_video_60s: boolean;
  screenshot_pack: boolean;
  step_by_step_docs: boolean;
  interactive_demo?: boolean;
  support_snippet?: boolean;
  sales_material?: boolean;
  target_duration_sec: number;
};
