import type { DemoWorkflowGraph, GraphNode } from "../types/workflowGraph";

type GraphEditorProps = {
  graph: DemoWorkflowGraph;
  onChange: (graph: DemoWorkflowGraph) => void;
  onApprove: (graph: DemoWorkflowGraph) => void;
};

export function GraphEditor({ graph, onChange, onApprove }: GraphEditorProps) {
  function updateNode(nodeID: string, patch: Partial<GraphNode>) {
    onChange({
      ...graph,
      nodes: graph.nodes.map((node) => (node.id === nodeID ? { ...node, ...patch } : node)),
    });
  }

  return (
    <section aria-label="Workflow graph editor">
      <header>
        <h2>HumanApprove</h2>
        <button type="button" onClick={() => onApprove(graph)}>
          Approve
        </button>
      </header>
      <ol>
        {graph.nodes.map((node) => (
          <li key={node.id}>
            <strong>{node.id}</strong>
            <span>{node.action}</span>
            <label>
              <input
                type="checkbox"
                checked={node.has_zoom}
                onChange={(event) => updateNode(node.id, { has_zoom: event.currentTarget.checked })}
              />
              Zoom
            </label>
            <label>
              <input
                type="checkbox"
                checked={node.is_screenshot}
                onChange={(event) => updateNode(node.id, { is_screenshot: event.currentTarget.checked })}
              />
              Screenshot
            </label>
          </li>
        ))}
      </ol>
    </section>
  );
}
