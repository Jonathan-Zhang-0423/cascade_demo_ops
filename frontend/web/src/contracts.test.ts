import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type {
  ClientExecutionPackage,
  ExchangeEnvelope,
  EncryptedPayloadRef,
  RecordingResultPackage,
} from "../../src/types/workflowGraph";

const contractBase = new URL("../../../contracts/exchange/v1/", import.meta.url);

describe("exchange contract fixtures", () => {
  it("loads client execution package fixtures without raw secrets", () => {
    const pkg = readFixture<ClientExecutionPackage>("client_execution_package.happy.json");
    const payload = JSON.stringify(pkg);

    expect(pkg.schema_version).toBe("demoops.client_execution_package.v1");
    expect(pkg.executable_script_bundle?.script_manifest.entry_function).toBe("runCascadeRecording");
    expect(pkg.executable_script_bundle?.script_manifest.runtime).toBe("playwright-restricted-sandbox");
    const safety = recordOf(pkg.safety_report);
    expect(safety.allowed_to_upload).toBe(true);
    expect(safety.upload_mode).toBe("structure_summary_only");
    expect(payload).not.toMatch(/raw-password|Bearer\s+|Authorization:|Cookie:|sk-|BEGIN PRIVATE KEY|\.env|0{6}/i);
  });

  it("keeps upload payload_ref aligned with envelope payload_ref", () => {
    for (const fileName of ["upload_plaintext_dev.json", "upload_encrypted_metadata_only.json"]) {
      const upload = readFixture<{
        upload_id: string;
        envelope: ExchangeEnvelope;
        payload_ref: EncryptedPayloadRef;
        payload?: unknown;
      }>(fileName);

      expect(upload.upload_id).toBeTruthy();
      expect(upload.payload_ref).toEqual(upload.envelope.payload_ref);
      expect(upload.payload_ref.encrypted).toBe(true);
      expect(upload.payload_ref.sensitive).toBe(true);
      if (fileName === "upload_plaintext_dev.json") {
        expect(upload.payload).toBeTruthy();
      } else {
        expect(upload.payload).toBeUndefined();
      }
    }
  });

  it("loads generated and failed result fixtures for UI mapping", () => {
    const generated = readFixture<RecordingResultPackage>("recording_result.generated.json");
    const failed = readFixture<RecordingResultPackage>("recording_result.failed.json");

    expect(generated.status).toBe("generated");
    expect(generated.delivery?.asset_refs?.[0]?.role).toBe("final_demo_video");
    expect(generated.delivery?.asset_refs?.every((artifact) => artifact.encrypted && artifact.sensitive)).toBe(true);

    expect(failed.status).toBe("failed");
    expect(failed.failure_diagnostic?.schema_version).toBe("demoops.script_failure_diagnostic.v1");
    expect(failed.failure_diagnostic?.screenshot_refs?.every((artifact) => artifact.encrypted && artifact.sensitive)).toBe(true);
    expect(failed.repair_request?.approval_required).toBe(true);
  });

  it("requires repair fixtures to carry lineage", () => {
    const repair = readFixture<ClientExecutionPackage>("client_execution_package.repair.json");
    const safety = recordOf(repair.safety_report);
    const approval = recordOf(safety.human_approval);

    expect(repair.repair_context?.source_result_id).toBe("result_contract_failed");
    expect(repair.executable_script_bundle?.repair_lineage?.base_bundle_hash_sha256).toBe("sha256:bundle");
    expect(approval.approval_id).toBeTruthy();
  });
});

function readFixture<T>(fileName: string): T {
  return JSON.parse(readFileSync(new URL(fileName, contractBase), "utf8")) as T;
}

function recordOf(value: unknown): Record<string, unknown> {
  expect(value).toBeTruthy();
  expect(typeof value).toBe("object");
  return value as Record<string, unknown>;
}
