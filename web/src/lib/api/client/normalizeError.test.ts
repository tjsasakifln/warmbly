import { describe, expect, it } from "vitest";

import { normalizeError } from "./normalizeError";

describe("normalizeError", () => {
    it("preserves structured API error details", () => {
        const details = {
            total: 4,
            failed: 4,
            quality: { blocked: true, bad_address_ratio: 0.75 },
        };

        const result = normalizeError({
            isAxiosError: true,
            response: {
                status: 422,
                data: {
                    error: "Unprocessable",
                    message: "Import blocked.",
                    code: "contact_import_quality_blocked",
                    request_id: "req_import_blocked",
                    details,
                },
            },
        });

        expect(result).toMatchObject({
            status: 422,
            code: "contact_import_quality_blocked",
            request_id: "req_import_blocked",
            details,
        });
    });
});
