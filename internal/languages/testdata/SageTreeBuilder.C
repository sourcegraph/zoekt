/*
 * Test data file for C++ language detection with .C extension
 * 
 * This file was copied from the ROSE compiler project for testing purposes only.
 * Original source: https://github.com/rose-compiler/rose/blob/weekly/src/frontend/Experimental_General_Language_Support/SageTreeBuilder.C
 * Downloaded: August 5, 2025
 * Commit: 420d0608781b8485953c7dd9f81cc608d0bbe898 (weekly branch)
 * 
 * Used to test that .C files containing C++ code are correctly detected as C++ language
 * instead of being misclassified as C by go-enry.
 * 
 * Original file is part of the ROSE project (https://github.com/rose-compiler/rose)
 * and is subject to their license terms.
 */

#include "sage3basic.h"
#include "rose_config.h"

#include "SageTreeBuilder.h"
#include "Jovial_to_ROSE_translation.h"
#include "ModuleBuilder.h"

#include <boost/optional/optional_io.hpp>
#include <iostream>

namespace Rose {
namespace builder {

using namespace Rose::Diagnostics;

namespace SB = SageBuilder;
namespace SI = SageInterface;
namespace LT = LanguageTranslation;

/// Initialize the global scope and push it onto the scope stack
///
SgGlobal* initialize_global_scope(SgSourceFile* file)
{
 // Set the default for source position generation to be consistent with other languages (e.g. C/C++).
    SageBuilder::setSourcePositionClassificationMode(SageBuilder::e_sourcePositionFrontendConstruction);

    SgGlobal* globalScope = file->get_globalScope();
    ASSERT_not_null(globalScope);
    ASSERT_not_null(globalScope->get_parent());

 // Fortran and Jovial are case insensitive
    globalScope->setCaseInsensitive(true);

    ASSERT_not_null(globalScope->get_endOfConstruct());
    ASSERT_not_null(globalScope->get_startOfConstruct());

 // Not sure why this isn't set at construction
    globalScope->get_startOfConstruct()->set_line(1);
    globalScope->get_endOfConstruct()->set_line(1);

    SageBuilder::pushScopeStack(globalScope);

    return globalScope;
}

void
SageTreeBuilder::attachComments(SgLocatedNode* node, bool at_end)
{
  PosInfo pos{node};
  attachComments(node, pos, at_end);
}

void
SageTreeBuilder::attachComments(SgExpressionPtrList const &list)
{
  auto jovialStyle{PreprocessingInfo::JovialStyleComment};

  for (auto expr : list) {
    PosInfo exprPos{expr};
    auto commentToken = tokens_->getNextToken();

    // May have problems with multi-line expressions, currently biased to comments following the expression
    if (commentToken && exprPos.getEndLine() == commentToken->getStartLine()) {
      auto commentLocation = PreprocessingInfo::after;
      if (exprPos.getStartCol() >= commentToken->getEndCol()) {
        commentLocation = PreprocessingInfo::before;
      }
      auto info = SI::attachComment(expr, commentToken->getLexeme(), commentLocation, jovialStyle);
      setCommentPositionAndConsumeToken(info);
    }
  }
}

void
SageTreeBuilder::attachComments(SgLocatedNode* node, const PosInfo &pos, bool at_end)
{
  PreprocessingInfo* info{nullptr};
  auto jovialStyle{PreprocessingInfo::JovialStyleComment};

  // Global scope first to catch beginning and terminating comments
  if (isSgGlobal(node)) {
    boost::optional<const Token&> token{};
    // Comments before START line, which is beginning of the global scope
    while ((token = tokens_->getNextToken()) && token->getStartLine() < pos.getStartLine()) {
      info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::before, jovialStyle);
      setCommentPositionAndConsumeToken(info);
    }
    // Comments same START line
    while ((token = tokens_->getNextToken()) && token->getStartLine() == pos.getStartLine()) {
      if (token->getStartCol() < pos.getStartCol()) {
        // Before START
        info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::before_syntax, jovialStyle);
      }
      else {
        // After START
        info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::after_syntax, jovialStyle);
      }
      setCommentPositionAndConsumeToken(info);
    }

    // Comments same end line
    while ((token = tokens_->getNextToken()) && token->getEndLine() == pos.getEndLine()) {
      if (token->getEndCol() > pos.getEndCol()) {
        info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::end_of, jovialStyle);
        setCommentPositionAndConsumeToken(info);
      }
    }
    // Comments after
    while ((token = tokens_->getNextToken()) && token->getEndLine() > pos.getEndLine()) {
      info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::after, jovialStyle);
      setCommentPositionAndConsumeToken(info);
    }
    return;
  }

  // Attach comments at end of a statement or expression
  if (at_end && (isSgStatement(node) || isSgExpression(node))) {
    boost::optional<const Token&> token{};

    // If a scope, some comments should be attached to last statement in scope
    SgStatement* last{nullptr};
    if (auto scope = isSgScopeStatement(node)) {
      last = scope->lastStatement();
    }

    while ((token = tokens_->getNextToken()) && token->getStartLine() <= pos.getEndLine()) {
      if (last && token->getEndLine() < pos.getEndLine()) {
        info = SI::attachComment(last, token->getLexeme(), PreprocessingInfo::after, jovialStyle);
      }
      else {
        info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::end_of, jovialStyle);
      }
      setCommentPositionAndConsumeToken(info);
    }
    return;
  }

  if (isSgScopeStatement(node)) {
    boost::optional<const Token&> token{};
    // Comments before scoping unit
    while ((token = tokens_->getNextToken()) && token->getStartLine() < pos.getStartLine()) {
      info = SI::attachComment(node, token->getLexeme(), PreprocessingInfo::before, jovialStyle);
      setCommentPositionAndConsumeToken(info);
    }
    return;
  }

  if (SgStatement* stmt = isSgStatement(node)) {
    boost::optional<const Token&> token{};
    while ((token = tokens_->getNextToken()) && token->getStartLine() <= pos.getStartLine()) {
      SgLocatedNode* commentNode{stmt};
      if (token->getTokenType() == JovialEnum::comment) {
        auto commentPosition = PreprocessingInfo::before;
        if (token->getStartLine() == pos.getStartLine()) {
          commentPosition = PreprocessingInfo::end_of;
          // check for comment following a variable initializer
          if (SgVariableDeclaration* varDecl = isSgVariableDeclaration(stmt)) {
            for (SgInitializedName* name : varDecl->get_variables()) {
              if (SgInitializer* init = name->get_initializer()) {
                PosInfo initPos{init};
                if (initPos.getEndCol() > token->getStartCol()) {
                  // attach comment after this variable initializer
                  commentNode = init;
                  break;
                }
              }
            }
          }
        }
        info = SI::attachComment(commentNode, token->getLexeme(), commentPosition, jovialStyle);
      }
      setCommentPositionAndConsumeToken(info);
    }
  }
  else if (auto expr = isSgEnumVal(node)) {
    boost::optional<const Token&> token{};
    // try only attaching comments from same line (what about multi-line comments)
    while ((token = tokens_->getNextToken()) && token->getStartLine() <= pos.getStartLine()) {
      if (token->getTokenType() == JovialEnum::comment) {
        if (token->getStartLine() < pos.getStartLine() || token->getEndCol() < pos.getStartCol()) {
          info = SI::attachComment(expr, token->getLexeme(), PreprocessingInfo::before, jovialStyle);
        }
        else {
          info = SI::attachComment(expr, token->getLexeme(), PreprocessingInfo::after, jovialStyle);
        }
      }
      setCommentPositionAndConsumeToken(info);
    }
  }

  else if (isSgJovialTablePresetExp(node)) {
    auto exprList = isSgJovialTablePresetExp(node)->get_preset_list()->get_expressions();
    attachComments(exprList);
  }

// TODO: Not ready yet until testing comments for expressions
#if 0
  else {
    // Additional expressions?
    mlog[WARN] << "SageTreeBuilder::attachComment: not adding node " << node->class_name() << "\n";
  }
#endif
}

/** Attach comments from a vector */
void
SageTreeBuilder::attachComments(SgLocatedNode* node, const std::vector<Token> &tokens, bool at_end) {
  auto commentPosition{PreprocessingInfo::before};
  if (at_end) commentPosition = PreprocessingInfo::after;

  for (auto token : tokens) {
    SI::attachComment(node, token.getLexeme(), commentPosition, PreprocessingInfo::JovialStyleComment);
  }
}

