import math
import unittest

from reports import metrics, joint_metrics, evaluation_report
from train import LABELS


def certain(names, name):
    return [float(label == name) for label in names]


class ReportContracts(unittest.TestCase):
    def test_minority_failure_and_missing_class_are_visible(self):
        result = metrics([0, 0, 0, 1], [[.9, .1, 0]] * 4, ['common', 'rare', 'absent'])
        self.assertEqual(result['accuracy'], .75)
        self.assertEqual(result['balanced_accuracy'], .5)
        self.assertEqual(result['per_class']['rare']['recall'], 0)
        self.assertIsNone(result['per_class']['absent']['recall'])
        self.assertEqual(result['unevaluated_classes'], ['absent'])
        self.assertAlmostEqual(result['macro_f1'], (6 / 7) / 3)
        self.assertAlmostEqual(result['supported_macro_f1'], (6 / 7) / 2)
        self.assertAlmostEqual(result['brier'], .42)
        self.assertAlmostEqual(result['ece_10_bins'], .15)
        self.assertEqual(result['error_at_0.8'], .25)

    def test_calibration_boundaries_are_counted_once(self):
        result = metrics([0, 1, 0, 1], [[.6, .4], [.4, .6], [.8, .2], [1., 0.]], ['a', 'b'])
        self.assertAlmostEqual(result['ece_10_bins'], .5)
        self.assertEqual(result['coverage_at_0.8'], .5)
        self.assertEqual(result['error_at_0.8'], .5)

    def test_invalid_distributions_do_not_enter_report(self):
        for p in ([float('nan'), 0], [math.inf, 0], [-.1, 1.1], [.4, .4], [1.], [0., 0.]):
            with self.subTest(p=p), self.assertRaises(ValueError):
                metrics([0], [p], ['a', 'b'])
        with self.assertRaises(ValueError):
            metrics([0, 1], [[1., 0]], ['a', 'b'])

    def test_no_accepted_predictions_has_no_error_estimate(self):
        result = metrics([0], [[.6, .4]], ['a', 'b'])
        self.assertEqual(result['coverage_at_0.8'], 0)
        self.assertIsNone(result['error_at_0.8'])

    def test_joint_policy_matches_retention_and_type_rules(self):
        rows = [{'retention': 'ignore', 'kind': None},
                {'retention': 'uncertain', 'kind': None},
                {'retention': 'durable', 'kind': 'fact'},
                {'retention': 'durable', 'kind': 'fact'},
                {'retention': 'temporary', 'kind': 'project'}]
        retention = [certain(LABELS['retention'], r['retention']) for r in rows]
        kind = [[1 / 6] * 6, certain(LABELS['kind'], 'fact'), [1 / 6] * 6,
                [.8, .2, 0, 0, 0, 0], certain(LABELS['kind'], 'fact')]
        result = joint_metrics(rows, retention, kind, LABELS)
        self.assertEqual(result, {'n': 5, 'accepted': 3, 'errors': 1, 'coverage': .6, 'error': 1 / 3})

    def test_majority_is_frozen_from_train_and_provenance_stays_separate(self):
        rows = [{'split': 'train', 'retention': 'durable', 'kind': 'fact', 'label_source': 'synthetic'},
                {'split': 'dev', 'retention': 'temporary', 'kind': 'project', 'label_source': 'teacher'},
                {'split': 'test', 'retention': 'temporary', 'kind': 'project', 'label_source': 'teacher'}]
        distributions = {name: [certain(names, row[name]) for row in rows] for name, names in LABELS.items()}
        report = evaluation_report(rows, distributions, LABELS)
        self.assertEqual(report['majority_baseline']['retention']['label'], 'durable')
        self.assertEqual(report['majority_baseline']['retention']['test']['accuracy'], 0)
        self.assertEqual(report['retention']['test']['accuracy'], 1)
        self.assertNotIn('test', report['by_label_source']['synthetic']['retention'])
        self.assertNotIn('train', report['by_label_source']['teacher']['retention'])
        self.assertEqual(report['by_label_source']['teacher']['joint_policy']['test']['n'], 1)


if __name__ == '__main__':
    unittest.main()
