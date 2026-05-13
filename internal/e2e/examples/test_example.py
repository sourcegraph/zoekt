import unittest

class TestSimpleOperations(unittest.TestCase):
    def test_addition(self):
        self.assertEqual(2 + 2, 4)

    def test_string_upper(self):
        self.assertEqual('hello'.upper(), 'HELLO')

if __name__ == '__main__':
    unittest.main()
