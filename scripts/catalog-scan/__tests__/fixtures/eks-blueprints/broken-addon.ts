// Fixture: broken-addon — TS file with NO chart constants. Exercises the
// "no extract → skip + warn" path. Defensive metadata extractor must not
// fabricate a proposal here.
import { Construct } from 'constructs';

export class BrokenAddon {
    constructor(_scope: Construct) {
        // intentionally empty — no helm metadata declared.
    }
}
