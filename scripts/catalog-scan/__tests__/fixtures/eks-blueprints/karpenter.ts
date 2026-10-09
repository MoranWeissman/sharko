// Fixture: karpenter addon — uses defaultProps shape with OCI repo URL.
import { HelmAddOn, HelmAddOnUserProps } from '../helm-addon';

const defaultProps = {
    name: 'karpenter',
    chart: 'karpenter',
    namespace: 'karpenter',
    repository: 'oci://public.ecr.aws/karpenter',
    release: 'karpenter',
    version: '0.37.0',
    values: {},
};

export class KarpenterAddOn extends HelmAddOn {
    constructor(props?: HelmAddOnUserProps) {
        super({ ...defaultProps, ...props });
    }
}
