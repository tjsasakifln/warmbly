import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ResultStep } from "./ImportWizard";

describe("contact import result", () => {
    it("explains a list-quality block without implying contacts were written", () => {
        render(
            <ResultStep
                filename="contacts.csv"
                result={{
                    total: 4,
                    imported: 0,
                    updated: 0,
                    skipped: 0,
                    failed: 4,
                    started_at: "2026-08-25T10:00:00Z",
                    ended_at: "2026-08-25T10:00:01Z",
                    quality: {
                        invalid: 2,
                        disposable: 1,
                        role: 0,
                        risky_tld: 0,
                        bad_address_ratio: 0.75,
                        blocked: true,
                    },
                }}
            />,
        );

        expect(screen.getByText("Import blocked")).toBeInTheDocument();
        expect(
            screen.getByText("No contacts were written because the list exceeded the quality threshold."),
        ).toBeInTheDocument();
    });
});
