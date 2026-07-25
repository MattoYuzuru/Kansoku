import { ComponentLifecyclePage } from "./ComponentLifecyclePage";

export function Plugins() {
  return (
    <ComponentLifecyclePage
      title="Plugins"
      wireframe="Plugin tree; child lifecycle; version adoption; cold/stale reasons."
      componentKind="plugin"
      extraGapNote="A plugin parent/child tree and version-adoption breakdown are also not shown: the only topology endpoint (/api/v1/components/mcp/topology) is scoped to MCP-kind components, and there is no per-plugin version-adoption query."
    />
  );
}
