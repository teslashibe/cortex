"""Aggregate classifier diagnostics without exposing source text or example IDs."""
import math


def checked_probabilities(values, size):
    values = [float(p) for p in values]
    if (len(values) != size or any(not math.isfinite(p) or not 0 <= p <= 1 for p in values)
            or abs(sum(values) - 1) > 1e-6):
        raise ValueError('invalid probability distribution')
    return values


def metrics(y, probabilities, names):
    """Keep absent classes visible and assign every confidence to one ECE bin."""
    y, probabilities = list(y), list(probabilities)
    if not y or len(y) != len(probabilities) or not names or len(set(names)) != len(names):
        raise ValueError('nonempty aligned labels and predictions required')
    confusion = [[0] * len(names) for _ in names]
    bins = [[] for _ in range(10)]
    brier, accepted, errors, correct = 0., 0, 0, 0
    for truth, values in zip(y, probabilities):
        if truth != int(truth) or not 0 <= truth < len(names):
            raise ValueError('invalid true class')
        truth = int(truth)
        p = checked_probabilities(values, len(names))
        prediction = max(range(len(names)), key=p.__getitem__)
        confidence = p[prediction]
        hit = prediction == truth
        confusion[truth][prediction] += 1
        correct += hit
        brier += sum((v - (i == truth)) ** 2 for i, v in enumerate(p))
        bins[min(9, int(confidence * 10))].append((confidence, hit))
        if confidence >= .8:
            accepted += 1
            errors += not hit
    per_class = {}
    for i, name in enumerate(names):
        support, predicted, hits = sum(confusion[i]), sum(row[i] for row in confusion), confusion[i][i]
        per_class[name] = {
            'support': support, 'predicted': predicted, 'correct': hits,
            'precision': hits / predicted if predicted else None,
            'recall': hits / support if support else None,
            'f1': 2 * hits / (support + predicted) if support + predicted else 0.,
        }
    supported = [v for v in per_class.values() if v['support']]
    return {
        'n': len(y), 'accuracy': correct / len(y),
        'macro_f1': sum(v['f1'] for v in per_class.values()) / len(names),
        'supported_macro_f1': sum(v['f1'] for v in supported) / len(supported),
        'balanced_accuracy': sum(v['recall'] for v in supported) / len(supported),
        'brier': brier / len(y),
        'ece_10_bins': sum(abs(sum(hit - confidence for confidence, hit in bucket))
                           for bucket in bins) / len(y),
        'coverage_at_0.8': accepted / len(y),
        'error_at_0.8': errors / accepted if accepted else None,
        'labels': names, 'confusion': confusion, 'per_class': per_class,
        'unevaluated_classes': [name for name, v in per_class.items() if not v['support']],
    }


def joint_metrics(rows, retention, kind, labels):
    """Mirror the Go shadow policy: uncertain abstains; ignore needs no type."""
    if not rows or len(rows) != len(retention) or len(rows) != len(kind):
        raise ValueError('nonempty aligned rows and predictions required')
    accepted, errors = 0, 0
    for row, rp, kp in zip(rows, retention, kind):
        rp = checked_probabilities(rp, len(labels['retention']))
        kp = checked_probabilities(kp, len(labels['kind']))
        ri, ki = max(range(len(rp)), key=rp.__getitem__), max(range(len(kp)), key=kp.__getitem__)
        r, k = labels['retention'][ri], labels['kind'][ki]
        retained = r in ('durable', 'temporary')
        if r == 'uncertain' or rp[ri] < .8 or (retained and kp[ki] < .8):
            continue
        accepted += 1
        errors += r != row['retention'] or (retained and k != row.get('kind'))
    return {'n': len(rows), 'accepted': accepted, 'errors': errors,
            'coverage': accepted / len(rows), 'error': errors / accepted if accepted else None}


def evaluation_report(rows, distributions, labels):
    """Evaluate unchanged splits; choose constant baselines from training labels only."""
    if any(len(distributions[name]) != len(rows) for name in labels):
        raise ValueError('prediction count mismatch')
    majority = {}
    for name, names in labels.items():
        counts = [sum(r['split'] == 'train' and r.get(name) == label for r in rows) for label in names]
        if not sum(counts):
            raise ValueError('training labels required for constant baseline')
        majority[name] = max(range(len(names)), key=counts.__getitem__)

    def cohort(indices):
        out = {name: {} for name in labels}
        out['majority_baseline'] = {name: {'label': names[majority[name]]} for name, names in labels.items()}
        out['joint_policy'] = {}
        for split in ('train', 'dev', 'test'):
            selected = [i for i in indices if rows[i]['split'] == split]
            if not selected:
                continue
            out['joint_policy'][split] = joint_metrics(
                [rows[i] for i in selected], [distributions['retention'][i] for i in selected],
                [distributions['kind'][i] for i in selected], labels)
            for name, names in labels.items():
                typed = [i for i in selected if rows[i].get(name) is not None]
                if not typed:
                    continue
                truth = [names.index(rows[i][name]) for i in typed]
                out[name][split] = metrics(truth, [distributions[name][i] for i in typed], names)
                constant = [float(i == majority[name]) for i in range(len(names))]
                out['majority_baseline'][name][split] = metrics(truth, [constant] * len(typed), names)
        return out

    report = cohort(range(len(rows)))
    report['by_label_source'] = {
        source: cohort([i for i, row in enumerate(rows) if row['label_source'] == source])
        for source in sorted({r['label_source'] for r in rows})
    }
    return report
